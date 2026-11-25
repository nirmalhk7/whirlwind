import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

const script = fileURLToPath(new URL("./release.sh", import.meta.url));
const sha = "a".repeat(40);
const otherSHA = "b".repeat(40);
const archives = ["whirlwind-linux-amd64.tar.gz", "whirlwind-darwin-arm64.tar.gz"];

// No test invokes the real GitHub CLI or needs a token.
const github = `#!/usr/bin/env bash
set -euo pipefail
printf '%s\\n' "$*" >> "$STUB_LOG"
case "$*" in
  *'--method POST'*) exit "$STUB_CREATE_TAG_EXIT" ;;
  *'--method PATCH'*) echo 'https://github.com/nirmalhk7/whirlwind/releases/tag/v0.2.0' ;;
  *'/git/ref/heads/main'*)
    if [[ -f "$STUB_LOG.draft" ]]; then
      echo "$STUB_MAIN_AFTER_UPLOAD"
    else
      echo "$STUB_MAIN"
    fi ;;
  *'/git/ref/tags/'*) echo "$STUB_TAG_SHA" ;;
  *'/tags?per_page=100'*) printf '%s\\n' "$STUB_TAGS" ;;
  *'/releases?per_page=100'*)
    if [[ "$*" == *'--jq'* ]]; then
      printf '%s\\n' "$STUB_RELEASES"
    else
      printf '%s\\n' "$STUB_DRAFT"
    fi ;;
  'release create '*)
    touch "$STUB_LOG.draft"
    echo 'https://github.com/nirmalhk7/whirlwind/releases/edit/123' ;;
  *) exit 99 ;;
esac
`;

function fixture(t) {
  const directory = mkdtempSync(join(tmpdir(), "whirlwind-release-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const log = join(directory, "commands");
  const output = join(directory, "output");
  writeFileSync(log, "");
  writeFileSync(join(directory, "gh"), github, { mode: 0o755 });
  for (const archive of archives) {
    const result = spawnSync("tar", ["-czf", archive, "--files-from", "/dev/null"], {
      cwd: directory,
    });
    assert.equal(result.status, 0, result.stderr?.toString());
  }
  const draft = {
    id: 123,
    tag_name: "v0.2.0",
    html_url: "https://github.com/nirmalhk7/whirlwind/releases/edit/123",
    draft: true,
    prerelease: false,
    assets: [...archives, "SHA256SUMS"].map((name) => ({ name, state: "uploaded" })),
  };
  return {
    directory,
    draft,
    calls: () => readFileSync(log, "utf8").trim().split("\n").filter(Boolean),
    output: () => readFileSync(output, "utf8"),
    run: (overrides = {}, command = "publish") =>
      spawnSync("bash", [script, command], {
        encoding: "utf8",
        env: {
          ...process.env,
          GH_TOKEN: "",
          PATH: directory + delimiter + process.env.PATH,
          RELEASE_VERSION: "v0.2.0",
          RELEASE_ASSETS: directory,
          GITHUB_SERVER_URL: "https://github.com",
          GITHUB_REPOSITORY: "nirmalhk7/whirlwind",
          GITHUB_REF: "refs/heads/main",
          GITHUB_EVENT_NAME: "workflow_dispatch",
          GITHUB_SHA: sha,
          GITHUB_OUTPUT: output,
          STUB_LOG: log,
          STUB_TAGS: "v0.1.0",
          STUB_RELEASES: "v0.1.0",
          STUB_MAIN: sha,
          STUB_MAIN_AFTER_UPLOAD: sha,
          STUB_TAG_SHA: sha,
          STUB_CREATE_TAG_EXIT: "0",
          STUB_DRAFT: JSON.stringify([draft]),
          ...overrides,
        },
      }),
  };
}

test("accepts stable SemVer and rejects noncanonical or unsafe inputs", (t) => {
  const f = fixture(t);
  for (const version of ["v0.0.0", "v1.2.3", "v100.99.999"]) {
    assert.equal(f.run({ RELEASE_VERSION: version }, "plan").status, 0, version);
  }
  assert.equal(f.output(), "publish=true\n".repeat(3));
  for (const version of [
    "",
    "1.2.3",
    "v01.2.3",
    "v1.02.3",
    "v1.2.03",
    "v1.2",
    "v1.2.3-rc.1",
    "v1.2.3+build",
    "v1.2.3\n",
    "v1.2.3; uname",
  ]) {
    assert.match(f.run({ RELEASE_VERSION: version }, "plan").stderr, /stable SemVer/);
  }
  assert.equal(f.output(), "publish=true\n".repeat(3));
  assert.deepEqual(f.calls(), []);
});

test("rejects every non-main-dispatch context before making an API call", (t) => {
  const f = fixture(t);
  for (const context of [
    { GITHUB_EVENT_NAME: "push" },
    { GITHUB_EVENT_NAME: "pull_request", GITHUB_REF: "refs/pull/42/merge" },
    { GITHUB_EVENT_NAME: "pull_request_target" },
    { GITHUB_REF: "refs/heads/feature/main" },
    { GITHUB_REF: "refs/heads/Main" },
    { GITHUB_REF: "refs/tags/main" },
    { GITHUB_REPOSITORY: "fork/whirlwind" },
    { GITHUB_SERVER_URL: "https://github.example.com" },
  ]) {
    assert.equal(f.run(context, "plan").status, 0);
    const result = f.run(context);
    assert.equal(result.status, 1, result.stderr);
    assert.match(result.stderr, /requires a workflow dispatch/);
  }
  assert.throws(f.output, { code: "ENOENT" });
  assert.deepEqual(f.calls(), []);
});

test("rejects an invalid version or commit and a missing archive without API calls", (t) => {
  const f = fixture(t);
  assert.match(f.run({ RELEASE_VERSION: "v0.2.0-rc.1" }).stderr, /stable SemVer/);
  assert.match(f.run({ GITHUB_SHA: "main" }).stderr, /commit SHA is invalid/);
  rmSync(join(f.directory, archives[0]));
  assert.match(f.run().stderr, /Missing or empty release archive/);
  assert.deepEqual(f.calls(), []);
});

test("rejects duplicate tags, duplicate draft versions, older versions, and stale main before writes", (t) => {
  const f = fixture(t);
  for (const [overrides, error] of [
    [{ STUB_TAGS: "v0.2.0" }, /already has a tag or release/],
    [{ STUB_RELEASES: "v0.2.0" }, /already has a tag or release/],
    [{ STUB_TAGS: "v0.10.0" }, /must be greater/],
    [{ STUB_RELEASES: "v1.0.0" }, /must be greater/],
    [{ STUB_MAIN: otherSHA }, /main has moved/],
  ]) {
    const result = f.run(overrides);
    assert.equal(result.status, 1, result.stderr);
    assert.match(result.stderr, error);
  }
  assert.ok(f.calls().length);
  assert.ok(f.calls().every((call) => !/--method|release create/.test(call)));
});

test("a concurrent tag creation blocks draft creation", (t) => {
  const f = fixture(t);
  assert.equal(f.run({ STUB_CREATE_TAG_EXIT: "22" }).status, 22);
  assert.match(f.calls().at(-1), /--method POST/);
  assert.ok(f.calls().every((call) => !call.startsWith("release create")));
});

test("refuses to publish after main or the tag changes or draft assets are missing", (t) => {
  const f = fixture(t);
  for (const [overrides, error] of [
    [{ STUB_MAIN_AFTER_UPLOAD: otherSHA }, /main has moved/],
    [{ STUB_TAG_SHA: otherSHA }, /no longer points/],
    [
      { STUB_DRAFT: JSON.stringify([{ ...f.draft, assets: [] }]) },
      /Could not verify the new draft/,
    ],
    [
      { STUB_DRAFT: JSON.stringify([{ ...f.draft, draft: false }]) },
      /Could not verify the new draft/,
    ],
  ]) {
    const result = f.run(overrides);
    assert.equal(result.status, 1, result.stderr);
    assert.match(result.stderr, error);
  }
  assert.ok(f.calls().some((call) => call.startsWith("release create")));
  assert.ok(f.calls().every((call) => !call.includes("--method PATCH")));
});

test("creates the exact tag, stages all assets and publishes only the verified draft", (t) => {
  const f = fixture(t);
  const result = f.run({ STUB_TAGS: "v0.1.10\nv0.2.0-rc.1" });
  assert.equal(result.status, 0, result.stderr);
  assert.deepEqual(
    f.calls().map((call) => call.replace(/\s--jq .*$/, "")),
    [
      "api --hostname github.com --paginate repos/nirmalhk7/whirlwind/tags?per_page=100",
      "api --hostname github.com --paginate repos/nirmalhk7/whirlwind/releases?per_page=100",
      "api --hostname github.com repos/nirmalhk7/whirlwind/git/ref/heads/main",
      `api --hostname github.com --method POST repos/nirmalhk7/whirlwind/git/refs --raw-field ref=refs/tags/v0.2.0 --raw-field sha=${sha} --silent`,
      `release create v0.2.0 ${archives.join(" ")} SHA256SUMS --repo github.com/nirmalhk7/whirlwind --verify-tag --draft --generate-notes --target ${sha} --title v0.2.0`,
      "api --hostname github.com --paginate repos/nirmalhk7/whirlwind/releases?per_page=100",
      "api --hostname github.com repos/nirmalhk7/whirlwind/git/ref/heads/main",
      "api --hostname github.com repos/nirmalhk7/whirlwind/git/ref/tags/v0.2.0",
      `api --hostname github.com --method PATCH repos/nirmalhk7/whirlwind/releases/123 --raw-field tag_name=v0.2.0 --raw-field target_commitish=${sha} --field draft=false`,
    ],
  );
  const checksums = spawnSync("sha256sum", ["--check", "SHA256SUMS"], { cwd: f.directory });
  assert.equal(checksums.status, 0, checksums.stderr?.toString());
});
