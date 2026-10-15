# Go adapter and onboarding verification

Verified on October 12, 2026 with Go 1.27.1 on macOS arm64 and installed `codex-cli 0.160.0`.

## Results

| Verification | Result |
| --- | --- |
| `go test -race -count=1 ./...` | Passed all Go packages |
| `go vet ./...` | Passed |
| Real Codex new-thread prompt and streamed reply | Passed; 18 reply deltas, exact random marker returned |
| Real Codex resume in a second adapter invocation | Passed; same thread ID, previous marker recalled without including it in second prompt, 18 reply deltas |
| Go HTTP onboarding through persisted agent result | Passed for built-in native Codex and registered custom ACP |
| Existing browser ACP fixture initialization | Passed standalone subprocess check with `protocolVersion: 1` |

Live verification calls `ProcessAdapter.Run`, launching `codex app-server --listen stdio://` using existing Codex authentication and configured model. Each turn uses a temporary read-only working directory and a two-minute timeout. The test requires streamed message events, successful turn completion, exact replies, and a stable resumed thread ID. It records the actual CLI version. Missing executable, authentication, quota, network, or protocol failures fail an enabled test.

Go onboarding tests use real HTTP handlers, temporary SQLite storage, committed project policy, real Git operations, workflow routing, and provider subprocess transport. GitHub commands, remote fetch, and agent inference are isolated fixtures. Tests register providers/projects, submit HTTP ideation, and verify four resulting directions plus stored final reply, provider identity, and session ID. They also cover invalid provider input, unavailable executable, changed/missing local policy, and preservation of existing provider configuration at startup. Existing HTTP tests cover mutation idempotency and version conflicts.

## Fixes and regression coverage

- ACP session loading previously appended replayed assistant history to the current reply. Clear replayed output after loading, while retaining event delivery.
- ACP loading previously discarded returned mode metadata, bypassing read-only `ask` mode selection on resumed sessions. Decode that response and apply mode selection.
- Reject unsupported/missing ACP protocol versions and empty ACP/Codex session identifiers before sending a prompt.
- Synchronize session identifier writes with adapter output access.

Protocol fixtures validate initialization order, client identity, ACP capabilities, working directory, new/load/resume requests, read-only policy, and exact outgoing Unicode/multiline prompt content. Tests assert ordered reply assembly and exercise question answers, permission allow/deny responses, session-load history replay, empty load responses, unsupported load capability, authentication RPC errors, failed/interrupted/refused turns, malformed frames, subprocess exit, and cancellation.

Codex protocol shapes were checked against the installed CLI's generated JSON schema and [official app-server documentation](https://learn.chatgpt.com/docs/app-server). ACP replay behavior follows [ACP session setup](https://agentclientprotocol.com/protocol/v1/session-setup).

## Reproduce

```sh
go test -race -count=1 ./...
go vet ./...
WHIRLWIND_TEST_CODEX_LIVE=1 go test -race -count=1 -v ./internal/provider -run '^TestCodexLiveRoundTrip$'
```

The live test skips unless explicitly enabled; a skipped run does not verify Codex inference. It uses existing login and creates a real Codex test conversation, consuming normal account usage. In the restricted Codex sandbox, process cleanup tests require permission to invoke `/bin/ps`; the full suite and live test passed outside that restriction.

Scope covers native Codex messaging, generic ACP contracts, Go registration onboarding, and persisted final replies. Interactive login automation, a Codex ACP bridge, browser E2E tests, and live HTTP/SSE text delivery were not tested. Workflow continues storing final replies rather than publishing streamed text through SSE.
