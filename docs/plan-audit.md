# PLAN.md implementation audit

> Historical audit of an earlier browser-interface and remote-publication
> implementation. The browser frontend has since been removed; current human
> interaction uses the Go/tview terminal dashboard. This document preserves
> the earlier review record and is not current setup guidance or acceptance
> evidence for the present implementation.

Audit date: October 12, 2026. Scope: the current working tree, including the
existing uncommitted Go refactors. Requirements repeated in PLAN.md are mapped
to the same implementation below. Examples illustrate the corresponding
requirement; they do not add separate product features.

All requirement groups have implementation paths. Local automated verification
passes. This is not a live compatibility certification of installed agents or
GitHub ingress: provider and GitHub boundaries use local fixtures in tests.
Antigravity is intentionally unavailable, as PLAN.md requires.

## Deployment and durable storage

| Requirement | Implementation / evidence |
| --- | --- |
| Single-user Go service, loopback control listener, same-origin web proxy | `cmd/whirlwind/config.go`, `cmd/whirlwind/listeners.go`, `web/app/api/v1/[...path]/route.ts`; control-security browser tests |
| SQLite through database/sql and modernc.org/sqlite | `internal/store/store.go`, `go.mod` |
| WAL, foreign keys, versioned migrations, one writer, short transactions, bounded busy retries | `store.Open`, `store.migrate`, `store.Transaction`; persistence and foreign-key tests |
| Durable tasks, plans, units/dependencies, leases, sessions, checkpoints, findings, checks, notifications, indexes, deliveries, receipts, events, outcomes | Versioned JSON resources plus indexed SQLite tables; writers in `internal/workflow`, `internal/httpapi`, and `internal/store`; model contracts in `internal/model/model.go` |
| Atomic resource changes and ordered events | `store.Put` / `put`, `store.BindIssue`, `store.SaveDirections` |
| Unique repository/issue identity for both webhooks and reconciliation | `issue_tasks` primary key; `store.IngestIssue`; issue deduplication and signed-webhook tests |
| Agent work outside transactions | Agent execution in `agentAttempt.execute`; store transactions contain database operations |
| Renewable fenced leases and child-process restart reconciliation | `internal/workflow/leases.go`, `store.Claim/Renew/Release`, `internal/process`; lease fencing, renewal, orphan and crash-before-PID tests |
| Separate webhook-only listener with signature validation | `cmd/whirlwind/listeners.go`, `Server.Webhoowhirlwinddler`, `internal/httpapi/webhook.go`; HMAC tampering test |
| Fresh SQLite store, no Python migration | Single current SQLite schema initialized directly; no upgrade migrations |

## Project and agent onboarding

| Requirement | Implementation / evidence |
| --- | --- |
| Add trusted local GitHub projects through web | `internal/httpapi/route_create_project.go`, `github.Check`, project form and local folder selector in `ControlCenter.tsx`; browser onboarding-to-PR and folder selection tests |
| Require installed, authenticated gh and GitHub repository | `github.Check` validates executable, authentication, origin, and canonical identity |
| Require committed, explicitly confirmed .whirlwind.yaml or .whirlwind.yml | `policy.Load`, `route_confirm_policy.go`, `Engine.trustedProject`; web confirmation control |
| Codex app-server, Cursor ACP, pinned official Claude SDK bridge | `provider.Defaults`, `providerSession.runCodex/runACP/runClaude`, `bridges/claude/bridge.mjs`, pinned package and lockfile |
| Provider setup and authentication procedure | `docs/go-backend.md`; web provider command/configuration controls; existing provider login is reused |
| Configurable generic ACP executable and argument arrays | `route_create_provider.go`, `provider.Input/Adapter`, `provider.Client`, web ACP registration form |
| One shared Go adapter interface | `provider.Adapter`; built-in and generic process adapters use it |
| Antigravity visibly unavailable without supported integration | Unavailable default provider and limitation message; browser inventory test |
| Recorded capabilities and quota, exclude incapable/unavailable/exhausted providers | `model.Provider`, `provider.Route`, `providerReady`, `provider.Quota`, quota events/retry; routing and missing-quota-window tests |
| Unknown quota remains unknown; confirmed exhaustion excludes until reset/recovery | `provider.Quota`, `providerReady`, `agentAttempt.retry`; quota handoff test |

## Ideation and planning

| Requirement | Implementation / evidence |
| --- | --- |
| Daily 10AM machine-time schedule; latest missed day only | `DueDay`, `Engine.Daily`, `Engine.Run`; timezone/DST scheduling test |
| Check repository changes and incrementally update versioned index before run | `Engine.index`, `context.Build`; incremental index test |
| Four distinct innovation directions per project | `ideationPrompt`, `generateDirections`, `validateDirections`, `store.SaveDirections` |
| Use cached index, GitHub issues/discussions, TODO markers, previous directions | `ideationPrompt`, `github.Sources`, `sourceEntry`, durable `source_cache` and `directions` resources |
| Prevent duplicate suggestions; ideation uses index without rereading repository | Normalized title validation against prior/current directions; read-only scratch directory and supplied index prompt |
| Git/text/symbol retrieval before optional embeddings | `context.Build`, `Index.Search`, `Index.Package`; Go AST and text symbol extraction |
| Embeddings enabled only after comparative recall improvement without criterion coverage loss | `Index.Evaluate`, `Benefit`, `agentRun.embedContext`, embedding-evaluation API; evaluation and invalidation tests |
| Display/select direction and start Planner Agent | `ControlCenter.tsx`, `route_select_direction.go`; selected-direction and browser workflow tests |
| Master plan with acceptance criteria and optional user questions | `step.planning`, `decodePlan`, `applyPlan`; planner question forms and browser answer/version test |
| Keep small plans one unit; persist dependency graph for larger plans | Planner contract/prompt, default main unit, `ValidateUnits`, durable plans/tasks/units; invalid graph tests |
| Run units only after dependencies finish | `readyUnits`, `dependenciesComplete`, `code`, durable unit stages; parallel integration test |
| Publish ready plan as whirlwind:ready issue before Worker Loop | `step.planReady`, `github.CreateIssue`, `store.BindIssue`; selected-direction publication and uncertain-write retry tests |

## Worker execution, verification, and review

| Requirement | Implementation / evidence |
| --- | --- |
| Start from signed issue webhooks; require whirlwind:ready label | `webhook.go`, `Issue.Eligible`, `Engine.Ingest`; signed webhook deduplication test |
| Periodic reconciliation instead of 30-second polling | `Engine.Reconcile`, five-minute default settings; same issue identity as webhook ingestion |
| Issue edits cancel/replan; closure or label withdrawal pauses with artifacts retained | `affectedByIssue`, `updateIssueTask`, `invalidateIssueTask`, `withdrawIssueTask`; old webhook, replan, eligibility restoration tests |
| Coding Agent receives issue plan in isolated branch/worktree | Planning and unit prompts, `prepareWorkspace`, `unitRun.prepareWorktree`; real Git workflow tests |
| Up to four global/two project agent processes by default | `Settings`, `Engine.acquire/release`, dispatch; web settings validation |
| Parallel units isolated; serial integration and Coding Agent conflict resolution | `runUnits`, `integrateUnits`, `cherryPickUnit`; parallel commits, sibling checkpoint, interrupted integration tests |
| Full deterministic verification before AI review | `finishCode`, `step.coding`, `step.review`, `Engine.verify`; failed checks never start review test |
| Formatting/build/tests/static analysis remain baseline checks at every risk | Committed policy check arrays, `verification.Select`; baseline preservation and highest-risk tests |
| Basic diff, scope, clean worktree checks | `checkedDiff`, `Engine.commit`, server-added diff/index/tracked-file checks |
| Failed diagnostics return to Coding Agent and required checks rerun after repair | `Handoff.FailingTests`, `step.repair`, stored verification reports; failed-check workflow test |
| Risk selected using paths/modules, changed APIs, and code types | Policy selectors, `changedAPISymbols`, `verification.Select`; path, API, code-type, documentation tests |
| Low/medium/high/critical retain configured baseline; additional tests supplied by project policy | `verification.Select`, `Policy.RiskRules`; policy example in `docs/go-backend.md` |
| High/critical require security and architecture review | `supplementReview`, `specialistReview`; critical workflow fixture routes specialist reviews |
| Notify Inbox when verified implementation transfers to review | `step.coding` review notification |
| Reviewer receives worktree, branch, plan, full diff, prior findings | `reviewSnapshot`, versioned handoff/context; read-only reviewer mutation checks |
| Review score out of 10, structured stable-ID findings persisted | `primaryReview`, `validateReviewFindings`, `saveReview`, `MergeFindings`; omitted blockers stay unresolved test |
| Check inaccessible/out-of-scope code against plan | Explicit full-diff review prompt plus deterministic path scope checks |
| Correct unresolved findings and review corrections without productive-pass cap | `step.repair` / `step.review`; productive review beyond five passes test |
| Five unchanged failures trigger fresh context/provider; three failed recoveries pause with Inbox decision | `Engine.progress`, recovery-aware routing/session selection; repeated-stall test |
| Acceptance requires score >=9/10, mandatory checks, no unresolved blockers | `Accepted`, `applyReview`; acceptance gate tests |
| Critical PR publication needs explicit web approval | `applyReview`, `approveTask`, publication gates; critical workflow and browser approval tests |
| Publish accepted branch as whirlwind:work PR with human-readable plan/implementation description | `step.publishing`, `github.CreatePR`, `prDescription`; issue-to-PR and unit-description tests |
| Notify Inbox with PR URL; user reviews/merges; never automatic merge | PR notification and link, `reconcilePR` observes human merge/closure; no merge operation in GitHub interface |

## Shared engines and web/API contracts

| Requirement | Implementation / evidence |
| --- | --- |
| Four agent roles, shared Workflow/Context/Decision logic in Go | `provider.Input.Role`, `Engine.agent`, context package selection and provider routing used by both loops |
| Durable checkpoints/commit references/completed criteria survive stop/provider switch | Task handoff, sessions/workspaces/units/checkpoints resources; interrupted integration and quota handoff tests |
| One versioned JSON handoff with branch/worktree, commits/checkpoints, completed work/criteria, failures/next action | `model.Handoff`, `agentRun.prepare`, `finishCode`, `applyReview` |
| Role context contains relevant code/interfaces/dependencies/tests; agent can request files | `Index.Package`, worktree index snapshots, ACP filesystem handler/native provider tools; filesystem bounds test |
| Progress records commits, verification results, errors, and configured run limit | Durable checkpoints/verification/agent_events/metrics, `Engine.progress`, `RunTimeoutSeconds`; configured-budget test |
| Cost and comparable historical outcomes inform capable-provider selection | `provider.Route`, `Engine.outcomes`, repository `Index.Category` |
| Record provider/tokens/cost/time/review attempts, checks, human PR decisions | `agentAttempt.finish`, verification records, `reconcilePR` outcomes |
| Measured success updates routing preferences | Comparable role/category outcomes after at least five observations, used by `agentRun.route` |
| Web adds projects/providers; shows directions, task/stage, review score/findings, PR link | `web/components/ControlCenter.tsx`; browser inventory and complete workflow tests |
| Web answers planner/native questions and permissions, approves critical publication, pauses/resumes tasks | ControlCenter forms/buttons, task/question/permission routes; hook cancellation, answer/version, resume-stage tests |
| Versioned /api/v1 contract and OpenAPI | `internal/httpapi`, `API.md`, `docs/openapi.json`; request contract table tests |
| Loopback/same-origin/host/request-size/idempotency/version protections | Next proxy, `validateControl`, `readMutation`, `readCurrent`, receipts; HTTP request, idempotency/version, browser control tests |
| Ordered resumable SSE events | `store.Events`, `eventCursor`, `streamEvents`, browser EventSource; replay cursor test |

## Verification results

- `go test -race ./...`: all packages pass.
- `go vet ./...`, `go build ./...`, `gofmt -l cmd internal`, `git diff --check`: pass; formatting/diff checks produce no findings.
- `npm run build` in `web`: production build and TypeScript checks pass.
- `npm run test:e2e` in `web`: nine tests pass, including the real local Go/SQLite/Git/ACP/Next direction-to-PR flow and native question round trip.
- `node --check bridges/claude/bridge.mjs`: syntax passes.
- `qlty check --all --no-fix --no-cache`: exits 0, with ten existing informational TODO/FIXME matches, no formatter/lint/security errors, and successful configured scanner executions.
- `qlty smells --all`: exits 0, 31 existing warnings versus 91 in the saved initial report; no new smell identities. New policy, project setup, and ACP initialization warnings were removed without relaxing rules or exclusions.

Generated scanner reports were removed during repository cleanup; the figures
above record that historical run. Existing smells are warnings, not a
zero-warning claim. Remaining live validation is the user's
authenticated agents, real GitHub writes, user-managed HTTPS webhook ingress,
and any configured external embedding endpoint. Those are not exercised by the
local sanity run.

## Resumed sanity check

Rechecked after Inbox became the default route, repository folder selection was
added, and provider/onboarding coverage expanded. The registered nine browser
tests pass, including lasting new-project setup instructions. Go race tests,
vet, builds, bridge syntax, formatting and diff checks pass.

Qlty's duplicated-literal and cognitive-complexity findings in protocol and
onboarding tests were resolved by extracting focused helpers while retaining
real transport, HTTP, Git, SQLite and workflow assertions. Policy loading,
project setup and ACP initialization were split into smaller phases. Final
Qlty check has only the ten existing informational matches; final smells has
31 baseline warnings and no new identities against the initial report.
An intermediate Trivy traversal failed while Playwright replaced its results
directory; the subsequent complete scan finished with exit code 0.

The current run does not repeat live account-backed inference or external
GitHub writes. Prior Codex inference evidence is recorded separately in
`docs/acp-codex-verification.md`.
