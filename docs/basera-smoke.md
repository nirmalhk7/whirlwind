# Basera workflow smoke test

From the Whirlwind checkout, with the Go version in `go.mod`, Node, Python 3,
Git, and Vim on PATH, run:

```sh
python3 scripts/basera-smoke.py
# Or use the committed HEAD of a local Basera checkout.
python3 scripts/basera-smoke.py /path/to/basera
```

The driver prints both revisions and uses private temporary state and a fresh
clone of Basera with its origin removed. It checks an intentionally malformed
README edit with `git diff --check`, repairs the edit, checks it again, and
restores the file. The shipped `start`, `dashboard`, `restart`, and `stop`
commands exercise the disposable project registration and process lifecycle.

The driver then checks the following boundaries with deterministic fixtures:

- Local ideation dispatch saves four README proposals against the committed
  Basera tree. The provider is a deterministic response adapter.
- The shipped dashboard opens a real PTY. It approves and archives separate
  ideas, checks that archiving queues no plan, and saves and separately approves
  a Vim edit. The Planner fixture exercises Markdown publication.
- A checks-green fixture receives a blocking first review. Reopening SQLite
  preserves the repair queue entry, finding, and approval, and fences an old
  worker's late result. The Linux launcher test also reaps an owned worker
  process on restart. A separate exact-candidate fixture exercises accepted
  local delivery.
- The Finalizer gate refuses recorded failed verification and a nonzero
  mandatory Qlty result in the fixture report. Acceptance also
  requires complete evidence with a matching candidate and command inventory.

These are separate fixture stages. The real README whitespace check runs
directly in the disposable clone. The driver uses a credential-free test
environment and the default sandbox setting. Proof of nono enforcement,
managed Qlty, live Codex, or a GitHub PR requires a live run.

Current `main` ends at local delivery. Live native qualification uses the
[configured ACP harness](acp-configured-acceptance.md) and
[nono setup](nono-setup.md). Draft
[#83](https://github.com/nirmalhk7/whirlwind/pull/83) records a continuous
Basera fixture run with offline ACP, rootless Docker, and pinned Qlty and
Gitleaks. The portable smoke driver can run independently of that draft. See
the [historical Basera results](basera-e2e-findings.md) for the prior live run.
