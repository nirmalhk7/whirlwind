# Configured ACP qualification (#6)

Recorded on 2026-11-17 with the user's existing `settings.yaml`, unchanged. **Live qualification is incomplete.** No Claude, Codex or Cursor inference/tool/resume/fallback certification is claimed.

The selected role was `plan`: Codex `gpt-6.1-sol`, `reasoning_effort=medium`. Settings SHA-256 was `9f4262a1be43ab5f7c210457e23b069d62613b9a4d53133f449486905eb7f867`. The file grants no provider network domains and its plan queue has one entry. No models, thinking choices, credentials, transport changes, network grants or fallback entries were added. Other role selections were not qualified.

## Recorded outcomes

| Stage | Claude | Codex | Cursor |
| --- | --- | --- | --- |
| Configured transport | Incomplete: `kind: claude` points to the legacy native bridge; current execution requires ACP. | Configured `kind: acp`, `acp_profile: codex` and installed Node adapter used. | Incomplete: `agent acp` command exists, but no explicit bundled ACP authentication profile is configured. |
| Model/thinking selection | No plan-role choice configured. | Exact configured model and thinking were confirmed in runtime output. This is settings confirmation, not inference certification. | No plan-role choice configured. |
| Inventory/catalog | Not qualified because configured transport is unsupported. | Passed: 9 advertised models, current `gpt-6.1-sol`. Metadata only. | Not qualified because configured authentication profile is absent. |
| Actual inference acceptance | Incomplete. | Failed: session established and completion arrived, but the reply did not equal the unpredictable marker. The final run emitted 2 streamed chunks and 300 reply bytes. Raw reply content is not recorded. | Incomplete. |
| Successful read tool | Incomplete. | Incomplete: inference prerequisite failed. | Incomplete. |
| Resume/context retention | Incomplete. | Incomplete: inference prerequisite failed. | Incomplete. |
| Configured fallback | Incomplete: no fallback chain. | Incomplete: no fallback chain. | Incomplete: no fallback chain. |
| Unsupported settings | Incomplete. | Passed: unsupported model rejected with advertised-model guidance; unsupported thinking rejected as not advertised for the selected model. | Incomplete. |
| Missing authorized authentication | Incomplete. | Passed: a temporary empty auth home and cleared profile auth references produced an actionable authentication setup error. Real credentials were not edited. | Incomplete. |

Earlier concurrent runs hit readiness/inference deadlines. The final recorded run, without concurrent heavy regression checks, rejected unsupported settings correctly and completed the configured prompt stage with a mismatched reply. The evidence does not establish the cause of that reply; the unchanged deny-all provider network policy remains part of the tested configuration. Permission broadening was not used as a repair.

## Exact runtime evidence

The tested platform was macOS 27.0.1 (build 26A434), Darwin 27.0.0 arm64, Go 1.27.1, Node v24.1.0 and native nono 0.81.0.

- Configured Codex adapter: `@agentclientprotocol/codex-acp@2.5.0`, installed at the exact path in `settings.yaml`.
- Actual ACP backend: bundled `@openai/codex/bin/codex.js`, reporting `codex-cli 0.165.0`.
- Host PATH Codex CLI: `codex-cli 0.167.0-alpha.4.1`. This is distinct from the ACP backend and is not substituted for its version.
- Installed native Claude CLI: `2.1.303 (Claude Code)`. This version check does not qualify an ACP adapter; no usable Claude ACP command is configured.
- Configured Cursor executable `agent`: `2026.10.22-27dcf5f`. This version check does not certify Cursor inference.

Only existing authorized native credential files/references are used by real runs. The negative authentication control changes the test process's temporary environment and restores it after the subtest; it never deletes or rewrites host credentials.

## Harness and reproduction

`internal/workflow/acp_configured_live_test.go` replaces the old default-based Codex live test. It reads provider commands, role choices and sandbox policy from the named settings file. Enabling it spends normal provider usage. An enabled incomplete stage fails; it is not silently skipped or labeled successful because a catalog responded.

```sh
WHIRLWIND_TEST_ACP_CONFIGURED_LIVE=1 \
WHIRLWIND_LIVE_SETTINGS="$PWD/settings.yaml" \
WHIRLWIND_LIVE_ROLE=plan \
go test -race ./internal/workflow -run '^TestConfiguredACPLiveAcceptance$' -count=1 -v -timeout=8m
```

The final recorded command exited 1 after 58.970 seconds, as expected for the incomplete outcomes above. Missing credentials/runtimes/configuration remain incomplete verification. A non-enabled ordinary regression run skips live qualification and cannot certify it.

Successful inference must return an exact random marker, confirmed model/thinking, a session ID, streamed reply events and completion. Resume must retain that session and recall the previous marker without repeating it in the second prompt. Tools must return unpredictable contents from a controlled file and expose a corresponding read-tool event under the unchanged read-only policy. All new workspaces and runtime data are temporary.

If the selected role has a configured fallback chain, the harness exercises the real workflow router: it injects only a primary quota failure and executes configured fallback choices through the real adapter, checking fresh session/workspace and exact model/thinking. This is a controlled primary-failure test, not a claim of live quota exhaustion. The current file has no fallback, so that path remains unverified; none was invented for the test.

The user explicitly chose to keep settings unchanged and record #6 as incomplete. Completing live certification requires usable configured Claude/Cursor ACP profiles and selections, authorized provider networking/authentication, successful inference/tool/resume results, and a configured fallback chain. The harness and recorded failures do not close that qualification gap.

## Repository regression limits

The final required script/bridge run passed all 79 tests. The execution package, focused protocol/auth/model/thinking checks, focused workflow connection/fallback checks, `go vet ./...`, both application builds and changed-file Qlty checks passed.

`go test -race -count=1 -timeout=20m ./...` exited 1: existing onboarding/dashboard/requalification and verification/workflow failures remain, and the workflow package reached its cumulative 20-minute timeout. Provider short-deadline failures passed focused reruns without changing deadlines. `qlty check --all --no-progress` also exited 1 with 340 findings, 24 security findings and one formatting finding outside the clean changed-file check. `qlty smells --all` exited 0 with repository-wide structural advisories. These results do not establish a green whole-repository regression.
