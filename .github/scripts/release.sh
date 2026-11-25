#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C

fail() {
  printf '%s\n' "$*" >&2
  exit 1
}

stable_tag='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
version=${RELEASE_VERSION:-}
repo=nirmalhk7/whirlwind

release_context() {
  [[ ${GITHUB_SERVER_URL:-} == 'https://github.com' &&
     ${GITHUB_REPOSITORY:-} == "$repo" &&
     ${GITHUB_REF:-} == 'refs/heads/main' &&
     ${GITHUB_EVENT_NAME:-} == 'workflow_dispatch' ]]
}

validate_version() {
  [[ $version =~ $stable_tag ]] ||
    fail 'Enter a stable SemVer tag in the form vMAJOR.MINOR.PATCH without leading zeros.'
}

if [[ ${1:-} == plan ]]; then
  if release_context; then
    validate_version
    printf 'publish=true\n' >> "${GITHUB_OUTPUT:?Set the Actions output file}"
  fi
  exit 0
fi
[[ ${1:-} == publish ]] || fail 'Use plan or publish.'
release_context || fail 'Publishing requires a workflow dispatch on nirmalhk7/whirlwind main.'
[[ ${GITHUB_SHA:-} =~ ^[a-f0-9]{40}$ ]] || fail 'The dispatch commit SHA is invalid.'
validate_version

api() {
  gh api --hostname github.com "$@"
}

require_current_main() {
  local main_sha
  main_sha=$(api "repos/$repo/git/ref/heads/main" --jq '.object.sha')
  [[ $main_sha == "$GITHUB_SHA" ]] ||
    fail 'main has moved since dispatch. Run the workflow again from current main.'
}

require_new_version() {
  local tags releases existing
  tags=$(api --paginate "repos/$repo/tags?per_page=100" --jq '.[].name')
  releases=$(api --paginate "repos/$repo/releases?per_page=100" --jq '.[].tag_name')
  while IFS= read -r existing; do
    [[ $version != "$existing" ]] || fail "$version already has a tag or release."
    if [[ $existing =~ $stable_tag ]]; then
      [[ $(printf '%s\n' "$version" "$existing" | sort -V | tail -n 1) == "$version" ]] ||
        fail "$version must be greater than existing stable tag or release $existing."
    fi
  done <<< "$tags"$'\n'"$releases"
}

cd "${RELEASE_ASSETS:?Set the staged asset directory}"
archives=(whirlwind-linux-amd64.tar.gz whirlwind-darwin-arm64.tar.gz)
for archive in "${archives[@]}"; do
  [[ -f $archive && -s $archive ]] || fail "Missing or empty release archive $archive."
  tar -tzf "$archive" >/dev/null
done
sha256sum "${archives[@]}" > SHA256SUMS

require_new_version
require_current_main

# Create the exact ref once. GitHub rejects a racing creation of the same tag.
api --method POST "repos/$repo/git/refs" --raw-field "ref=refs/tags/$version" \
  --raw-field "sha=$GITHUB_SHA" --silent
draft_url=$(gh release create "$version" "${archives[@]}" SHA256SUMS --repo "github.com/$repo" \
  --verify-tag --draft --generate-notes --target "$GITHUB_SHA" --title "$version")

# Work with the draft this run created and verify all uploads before publishing.
expected_assets=$(printf '%s\n' "${archives[@]}" SHA256SUMS | jq -R . | jq -s 'sort')
draft_id=$(api --paginate "repos/$repo/releases?per_page=100" |
  jq -ers --arg tag "$version" --arg url "$draft_url" --argjson assets "$expected_assets" '
    [ .[][] | select(.tag_name == $tag) ] |
    select(length == 1) | .[0] |
    select(.html_url == $url and .draft and (.prerelease | not)) |
    select(([.assets[] | select(.state == "uploaded") | .name] | sort) == $assets) |
    .id
  ') || fail 'Could not verify the new draft and all of its uploaded assets.'
[[ $draft_id =~ ^[0-9]+$ ]] || fail 'The new draft has an invalid release ID.'

require_current_main
tag_sha=$(api "repos/$repo/git/ref/tags/$version" --jq 'select(.object.type == "commit") | .object.sha')
[[ $tag_sha == "$GITHUB_SHA" ]] || fail "$version no longer points to the verified commit."
api --method PATCH "repos/$repo/releases/$draft_id" --raw-field "tag_name=$version" \
  --raw-field "target_commitish=$GITHUB_SHA" --field draft=false --jq '.html_url'
