# Restaurant ERP end-to-end test report

> Historical report from the former browser-interface implementation. The
> frontend and its browser test suite have since been removed. Results below
> describe the tested snapshot only; they are not current product verification
> or instructions for running Whirlwind today.

Date: October 14, 2026 (America/Denver).

**Result: scenario testing completed; production readiness not established.** Two controlled workflows reached `complete` through the real Go server and Next.js UI. GitHub was simulated throughout. Real repository checks exposed setup failures described below.

## Scope and preservation

- Implementation under test: Whirlwind implementation in this repository at the time of the test.
- Target: isolated Git clone plus current non-ignored files from the local `restaurant-erp` project, including its unfinished edits. Snapshot committed only in the disposable copy.
- Original target: 202 file fingerprints unchanged; no `.whirlwind.*`, `.qlty`, or `.codegraph` added. No original branch, index, or commits intentionally modified.
- Real boundaries: HTTP, Next.js proxy, browser, SQLite, ACP transport, Git worktrees/commits/cherry-picks, Qlty initialization/validation, CodeGraph initialization, repository typecheck/lint/tests/build, verification and review orchestration.
- Simulated boundaries: GitHub CLI responses, Git fetch/push, deterministic planning/coding/review agent. Published issue/PR URLs were fixture output, not real created issues/PRs. No real GitHub mutations occurred.
- Separate live checks: installed Codex inference and session resume; Codex and Claude ACP model discovery. These do not establish arbitrary agent performance on restaurant features.

## Findings

### P1 — Generated setup is missing from execution worktrees

Onboarding reports `Ready for execution` after committing `.whirlwind.yml` and `.qlty/qlty.toml` locally. Workflow worktrees start from `refs/remotes/origin/main`, which does not contain that local setup commit. The first generated Qlty check fails with:

> Qlty must be set up in this repository. Try: qlty init

Reproduced against the restaurant snapshot with its remote-tracking base deliberately preserved, as a normal unpushed onboarding commit would be. The fixture does not advance remote refs or push onboarding changes.

Relevant code: [worktree base selection](../internal/workflow/worktrees.go). Suggested fix direction: explicitly establish how committed setup reaches execution bases, or block readiness with a clear actionable condition. Do not silently publish user work to solve it.

### P2 — Qlty initialization findings are treated as setup failure

First registration returned HTTP 422 because `qlty init --yes --no-upgrade-check` created a valid configuration, then ran a sample scan and exited 99 with findings. A second registration succeeded using that existing configuration.

Relevant code: [configuration initialization](../internal/httpapi/project_setup.go). Distinguish configuration failure from scan findings while still validating generated configuration. Scanner findings were observed, not independently audited as vulnerabilities.

### Environment prerequisite — Fresh worktrees cannot complete the production build

Restaurant typecheck and lint passed; Vitest passed 31 files / 77 tests. `next build` compiled and typechecked but failed during page-data collection because `BETTER_AUTH_SECRET`, `BETTER_AUTH_URL`, and `STOREFRONT_RESTAURANT_SLUG` were absent. Ignored local environment files were intentionally not copied. No credentials were invented or extracted.

This requires a documented, authorized environment setup for worktrees; it is not proof that the application build fails with its intended environment.

### Additional observations

- Reconciliation initially uses a hard-coded five-minute next deadline. Changing the UI setting to 30 seconds did not reschedule that existing deadline; subsequent cycles use the configured interval. Restart performed immediate reconciliation. See [scheduler](../internal/workflow/engine.go).
- Raw ANSI escape sequences from build output appear in task failure details; logs would be easier to read if stripped or rendered.
- Tasks awaiting questions are intentionally considered idle for project deletion. Unregister preserved history; re-register restored continuation. An actual coding attempt waiting on permission correctly rejected deletion with 409.

## Verification results

| Check | Result |
| --- | --- |
| `go test -race -count=1 -json ./...` outside sandbox | 261 test/subtest passes, zero failures; two opt-in live tests skipped in this invocation |
| Separate `TestCodexLiveRoundTrip` | Passed new turn, streamed reply, resumed turn |
| Separate `TestACPModelCatalogLive` | Passed Codex (7 models) and Claude (5 models) |
| `go vet ./...` | Passed |
| Whirlwind `npm run build` | Passed |
| `node --test bridges/claude/bridge.test.mjs` | 4 passed |
| Existing Chromium suite `npm run test:e2e` | 22 passed, including desktop/mobile screenshots |
| Restaurant generated typecheck / lint | Passed |
| Restaurant generated Vitest | 31 files, 77 tests passed |
| Restaurant generated Qlty / build | Failed for reasons above; publication remained blocked |
| Final state assertions | Both tasks complete; merged and closed outcomes; allow and deny recorded; four directions retained; original files unchanged |

Initial cached Go run passed. The subsequent uncached sandbox run failed only two process-registry cases because `/bin/ps` was denied; unrestricted fresh run passed. Browser suite likewise required permission to bind localhost ports. Those infrastructure restrictions are not product regressions.

## Scenario coverage

| Scenario | Evidence / outcome |
| --- | --- |
| New target setup with real tools | Initial Qlty failure and successful retry recorded |
| Invalid repository path in browser | Actionable error; existing project preserved |
| Project registration, confirmation, unregister/re-register | Browser verified; custom smoke policy preserved |
| Four ideas, selection, planning | Four categories persisted; browser selected second direction |
| Duplicate ideas | Second manual generation rejected after three attempts; direction count stayed four |
| Planner question | Browser submitted answer; task resumed planning then coding |
| Unit worktree, integration, checks | Real worktree and file edit; generated checks recorded; isolated repository checkout unchanged by implementation |
| Failure visibility and pause/resume | Browser displayed Qlty/build failure; resume succeeded under separately identified smoke policy |
| Permission deny | Browser denial returned to ACP agent; task paused with explicit reason |
| Permission allow | Browser allow-once returned to ACP agent; implementation completed |
| Active project deletion | 409 while coding agent waited on permission |
| High/critical reviews | Main, security, architecture fixture reviews persisted with score 9 |
| Approval | Browser approval required before both simulated PR publications |
| Publication / merge / closure | Two simulated PRs; one merged, one closed; both tasks became complete |
| Restart and single-server lock | Duplicate instance rejected; graceful shutdown/restart retained state and reconciled merge |
| Settings errors | Invalid YAML visible in browser; restoring file and retrying recovered without restart |
| Provider connectivity | Fixture Hello World succeeded; intentionally disabled Codex fixture produced actionable failure |
| Run evidence | Exact persisted prompt, input snapshot, permission event, and linked decisions visible in browser |
| Responsive UI | Direct 390px Inbox check had scrollWidth 390; existing browser suite additionally checked desktop/mobile pages |
| API contracts | Replay, conflicting keys, stale versions, invalid capacity/timezone, unknown resource, forbidden completed-task actions, invalid evidence cursors/limits tested |
| Other supported edge paths | Fresh automated suite covers webhook validation, issue edits/withdrawal, leases/recovery, dependency graphs/integration, quota/model fallback, review blockers, publication provenance, embeddings fixtures, redaction, settings and persistence contracts |

The individual Go scenarios are defined in the repository test files. Historical Python-era browser specs are excluded by repository configuration and were not treated as current supported flows. This is bounded scenario coverage, not a claim to enumerate every possible failure or prove model judgment.

## Controlled continuation and limitations

After preserving the real generated-policy failures, the disposable copy received a clearly labeled smoke policy checking the exact fixture output and classifying the change as critical. This enabled review, approval, publication, merge/closure, and UI recovery tests. **Those successful smoke workflows do not override or repair the failed production checks.** Production implementation code was not changed.

## Evidence retention and reproduction

Generated screenshots, logs, scanner reports, and test binaries were removed during repository cleanup. This document preserves the observed results; it is not a fresh verification of later commits.

The verification commands and browser scenarios above applied to the historical snapshot only. Repeating the restaurant-specific experiment requires restoring that historical frontend/test harness in a disposable checkout, along with a disposable target snapshot, real Qlty/CodeGraph, and isolated settings/state with simulated GitHub writes. Do not register an unfinished original checkout merely to reproduce this report.

The temporary investigative harness and machine-specific state are excluded from version control. No production defects were fixed as part of report cleanup.
