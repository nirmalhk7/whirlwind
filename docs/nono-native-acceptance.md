# Native nono isolation acceptance (#7)

> Dated macOS evidence for the exact fixtures and runtime below. It does not
> qualify current HEAD or any Linux execution. See [current support](support.md).

## Coding workflow, 2026-11-19

The deterministic ACP peer ran through Coding, Reviewer, and Finalizer in
native nono 0.81.0 on macOS arm64. Coding added a regression test via the
ACP file callback, ran the approved check for red and green evidence, and
edited and deleted files through an ACP terminal. The independent runner and
local delivery recorded the same Git tree. The fallback fixture recorded four
primary attempts, clean shutdown before each replacement, and a successful
fallback with the same approved write scope.

The new regressions caught a subdirectory terminal shifting write grants,
repair receiving the wider project scope, a Coding clarification losing its
pause to a later Reviewer attempt, and conflict resolution requiring the agent
to write Git metadata. They passed after the fixes.

```sh
go test -race ./internal/workflow ./internal/provider ./internal/execution \
  -run '^Test(Coding|Adherence|FailedChecksDoNotStartReview|ParityCoding|ParityReviewerPreparation|ParityTwoCoding|ApprovedPlansForSameRepository|ParityAnsweredQuestion|AnswerQuestion|Nono|ACPTerminalCannotWriteOutsideSandbox|ACPFile)' \
  -count=1 -timeout=6m
```

Tested on 2026-11-17: macOS 27.0.1 (build 26A434), Darwin 27.0.0 arm64, nono 0.81.0, Node v24.1.0, Go 1.27.1. These results cover this native macOS boundary, not Linux or legacy OpenShell enforcement.

## Acceptance coverage

`bridges/nono/isolation.test.mjs` creates a committed temporary Git repository and two real linked worktrees. The selected worktree receives a whole-worktree coding grant; common Git metadata is readable, and a separate declared output directory is writable.

| Requirement | Executable evidence |
| --- | --- |
| Protect the linked worktree's `.git` pointer | Appending to the pointer fails with `EPERM`/`EACCES`; host bytes remain unchanged. |
| Protect shared refs and hooks | Writes to the source, selected and sibling branch refs, common `HEAD`, and a real hook fail; host bytes remain unchanged. |
| Protect shared and per-worktree configuration | Writes to common `config` and both linked worktrees' `config.worktree` fail. |
| Protect other worktrees | Writes to both primary and sibling source files and both worktree `HEAD` files fail. |
| Prevent symlink scope bypass | Links inside the writable selected worktree cannot redirect writes to a shared ref or sibling file. |
| Keep authorized writes working | Source creation inside the selected worktree and a declared output write both succeed. Every denied target exists and is host-writable before policy application. |
| Block direct UDP and DNS | Unconfined UDP and controlled DNS queries reach the local UDP fixture and return a hand-built `192.0.2.7` DNS answer. Sandboxed probes fail; fixture packet count stays unchanged. Direct DNS/53 is also denied. |
| Block SSH/22 bypass | Direct TCP/22 to loopback and the documentation address `192.0.2.7` fails with a kernel permission error, rather than connection refusal or timeout. |

The network probes enable a bounded HTTP domain grant, so their result is not limited to deny-all networking. Existing tests in `bridges/nono/launch.test.mjs` run alongside them: scoped credential-route POST succeeds with HTTP 201, prohibited methods and paths return 403, unapproved domains and HTTPS paths fail, and scoped MCP works without creating a public CONNECT bypass. Synthetic fixture credentials never leave those fixtures.

`TestNonoLinkedWorktreeReadsSharedMetadataWithoutWriteAuthority` separately runs the production Go `ForPolicy`/`Command` pipeline. Shared Git config must actually be readable inside native nono; writes to shared config and the primary worktree fail, while an authorized selected-worktree write succeeds. This catches a missing automatic metadata read grant or an overly broad parent grant.

## Verification

```sh
node --test bridges/nono/*.test.mjs
go test -race ./internal/execution -count=1
```

The native suite passed all 15 tests. The execution package passed with race detection. The two new native tests also passed independently after expanding coverage to all branch refs and per-worktree configuration (15 denied filesystem routes).

An additional mutation check replaced nono only in disposable test fixtures with a deliberately unconfined command wrapper. Both new tests failed: the Git pointer became writable and UDP returned `sent` instead of `denied`. Restoring actual nono made them pass. The wrapper is a test-discrimination control, not isolation certification.

No production permission changes were needed. All test repositories, files, sockets and datagrams are controlled disposable fixtures. Repository-wide regression results are recorded separately in commit verification evidence; a native boundary pass does not certify the entire application.
