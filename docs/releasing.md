# Releasing Whirlwind

The [Whirlwind CI and release workflow](../.github/workflows/release.yml) runs
launcher and bridge tests, vet, and builds on branch pushes and pull requests.
Linux race-tests the dashboard and control packages and checks that the
launcher refuses native execution. macOS runs the full Go race suite and the
sandbox suites with pinned nono and the locked legacy bridge dependencies.
The checks have read-only repository access. A manual run from another branch
also runs checks. Publication requires a manual run from `main` in
`nirmalhk7/whirlwind`.

## Publish from main

1. Open **Actions → Whirlwind CI and release → Run workflow** and select
   **main**. GitHub makes manual dispatch available after the workflow is on
   the default branch.
2. Enter the next stable tag as `vMAJOR.MINOR.PATCH`, for example `v0.2.0`.
   Use the major number for incompatible changes, minor for compatible
   features, and patch for compatible fixes. Each number is unpadded. The new
   version must be higher than all existing stable tags and releases.
3. Run the workflow. Both build jobs must pass on the dispatched commit.
   The publisher checks that the commit still heads `main`, creates a tag
   for that exact SHA, uploads the archives and checksums to a draft, and
   verifies the draft and ref before making it public.

The equivalent command is
`gh workflow run release.yml --repo nirmalhk7/whirlwind --ref main -f version=v0.2.0`.
Choose the version before running the command. A workflow token is the only
credential used by CI to publish. Tag rules and Actions permissions still
apply.

If a run stops before creating anything, dispatch again from current `main`.
If it stops after reserving a tag or creating a draft, inspect the run and
the partial release before retrying. The publisher refuses to reuse existing
tags or drafts. It never moves tags or replaces assets. GitHub's immutable
releases setting provides repository-wide protection after publication.

## Release assets

| Archive | Build runner | Runtime |
| --- | --- | --- |
| `whirlwind-linux-amd64.tar.gz` | Linux x86-64 | Dashboard inspection and supported opt-in offline verification |
| `whirlwind-darwin-arm64.tar.gz` | Apple silicon macOS | Native execution requires the locally configured runtime described in the support matrix |

Verify the downloaded archives with `sha256sum --check SHA256SUMS` (Linux)
or `shasum -a 256 -c SHA256SUMS` (macOS). Each archive contains the
`whirlwind`, `whirlwind-dashboard`, and `whirlwind-verify` Go commands.
Use the matching tagged checkout and the [launcher instructions](../README.md#run-locally)
for the complete workflow, including the Node launcher, bridges, and local
configuration. The archives have no release signature. See the
[support and verification matrix](support.md) for runtime prerequisites and
native nono, configured provider, and offline Docker acceptance.
