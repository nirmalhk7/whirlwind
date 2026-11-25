# Whirlwind remaining-work roadmap

Whirlwind is a single-user local AI orchestration platform with a Go server,
SQLite state, and a web control center. Optimize accepted, verified software per
whole-task token and operator minute. Delivery order: project isolation,
durability, bounded learning, then measured efficiency.

This document tracks unfinished work against the current working tree, including
uncommitted implementation. Completed onboarding, scheduling, workflow, operator
controls, storage, provider adapters, and baseline verification are removed from
the backlog. Existing code is not proof of live provider compatibility or savings.
Check current implementation before starting each item; extend it rather than
recreating it. P0 is required foundation, P1 follows that foundation, and P2 is
subsequent differentiation. Experiments are separate from release commitments.

Product constraints remain: local macOS/Linux, loopback control API, confirmed
project policy, explicit operator decisions, no automatic merge, and no silently
expanded authority. No hosted teams, multi-tenancy, mandatory VMs, model training,
or unrestricted cross-project memory.

Current behavior and interfaces live in [README.md](README.md),
[workflow modes](docs/workflow-modes.md),
[local orchestration](docs/local-orchestration.md), and [API.md](API.md).
Personal mode uses GitHub/`gh` and `whirlwind:ready`; Agentic mode uses local Git,
versioned approved plans, and configured native integrations without requiring
GitHub issues or `gh`. Both share implementation, verification, review, and
Submitter stages; the host validates and performs publication. Ideation uses the
configured schedule/timezone and bounded shortlist, which may be empty.

## Deployment and storage

### DUR-3 — Consistent backup, restore, and storage management [P0; new]

- **Depends on:** Existing SQLite/WAL store and artifact layout; DUR-1 defines
  recovery-critical references.
- **Remaining work:** Back up database and referenced plans, evidence, checkpoints,
  and runtime identities as one consistent recovery set. Provide integrity checks,
  restore into a separate data directory, schema-upgrade recovery instructions,
  disk-usage reporting, and previewable cleanup. Exclude reusable credentials
  from exports. Preserve artifacts needed by active tasks, uncertain mutations,
  approvals, and referenced lessons; never silently delete operator work.
- **Acceptance:** A restored task retains approved scope, evidence references,
  consumed/uncertain budgets, and pause state. Missing artifacts are reported and
  block unsafe execution. Cleanup explains retained dependencies before deletion.
- **Verification:** Restore real SQLite and Git fixtures into an isolated directory;
  test WAL consistency, interrupted backup, corruption, missing artifacts, disk
  exhaustion, migration recovery, and cleanup retention.

# Onboarding Projects

### ISO-3 — Project-scoped integration authority [P0; hardening]

- **Depends on:** ISO-1; existing mode-specific onboarding, native integration
  inventory, repository preflight, and destination validation.
- **Remaining work:** Audit every discovery/read/tool/mutation path against the
  selected project and operation. Enforce scope at the host broker rather than
  trusting prompt instructions or an agent-supplied project name. A connected
  integration must not expose unrelated repositories, documents, or account data.
  Block unsupported scope enforcement with actionable setup guidance.
- **Acceptance:** An agent cannot widen project scope by choosing another tool,
  connection, destination, or identifier. Existing validated publication controls
  remain intact; filesystem isolation is not presented as remote-data isolation.
- **Verification:** Extend integration contract tests with cross-project reads,
  forged resource identifiers, unknown tools, provider mismatch, and destination
  changes. Validate supported native connections separately from fixture agents.

# Onboarding AIs

### ISO-2 — Codex, Claude, Cursor, and ACP conformance [P0; validation]

- **Depends on:** ISO-1 and ISO-3; existing shared provider interface and adapters.
- **Remaining work:** Establish one versioned conformance suite for actual supported
  transports: Codex app-server, Claude SDK bridge, Cursor ACP, and generic ACP.
  Include explicit-thinking transport paths where applicable. Validate startup,
  authentication, scoped tools, child processes, cancellation, usage reporting,
  session compatibility, and missing-dependency behavior. Record tested versions
  and unsupported combinations rather than treating installation as readiness.
- **Acceptance:** Each advertised supported configuration works within the same
  project boundary on macOS/Linux or is clearly unavailable. No unrestricted
  fallback, automatic privilege expansion, or invented quota/token measurements.
  Antigravity remains unavailable until a supported integration exists.
- **Verification:** Reuse current provider and sandbox fixtures; add only missing
  cases. Run live compatibility checks under explicit budgets, with temporary
  projects and unrelated-file canaries. Metadata checks alone do not certify
  inference, sandbox behavior, or publication.

# System Overview

## Ideation Loop

### LEARN-1 — Evidence-backed lesson lifecycle [P1; new]

- **Depends on:** DUR-1, DUR-2, and existing acceptance/outcome evidence.
- **Remaining work:** Derive project-scoped lesson candidates from accepted
  changes, reproducible failures, explicit corrections, and feedback. Store
  trigger, scope, concise instruction, source revision, supporting artifacts,
  validation results, contradictions, lifecycle state, and version. Separate
  candidate generation from validation; budget both explicitly. Deduplicate,
  supersede, quarantine, expire stale candidates, and support inspect, correct,
  disable, delete, and rollback. Keep lesson references safe during cleanup.
- **Acceptance:** Automatically promote only after applicable deterministic checks
  and held-out evaluation demonstrate no acceptance regression. Missing evidence
  leaves a candidate quarantined. Relevant source changes, reverts, confirmed
  regressions, or contradictions invalidate affected lessons. Lessons cannot
  modify permissions, policy, credentials, approvals, or publication authority.
  Cross-project reuse requires explicit promotion of a sanitized rule.
- **Verification:** Fixtures cover independent validation, poisoning, conflicting
  feedback, duplicates, stale revisions, secret redaction, project separation,
  promotion, invalidation, deletion, and rollback. Live usefulness evaluation is
  budgeted and reported separately from lifecycle correctness.

## Worker Loop

### DUR-1 — Complete provider-independent recovery contract [P0; hardening]

- **Depends on:** Existing task/handoff/checkpoint models, leases, and ISO-1.
- **Remaining work:** Compare existing handoff fields and recovery behavior with a
  bounded recovery capsule: approved objective/scope, plan version, worktree and
  checkpoint references, completed criteria, unresolved findings, pending operator
  decisions, consumed/uncertain budgets, and next permitted action. Add only
  missing fields and consistency checks. Bind source, policy, execution-profile,
  context, and evidence identities; persist transitions atomically with events.
  Reconstruct fresh provider context from artifacts rather than full transcripts.
- **Acceptance:** A different eligible provider can continue from the latest valid
  checkpoint when a native session is unavailable. Changed scope or evidence
  causes revalidation; manual pause/global stop and operator edits survive.
- **Verification:** Extend existing session-selection and fallback fixtures with
  transcript-free restart and Codex/Claude/Cursor handoff scenarios. Verify
  completed work, unresolved blockers, budgets, and authority survive unchanged;
  distinguish simulated adapter handoff from live compatibility evidence.

### DUR-2 — Close recovery and external-effect gaps [P0; hardening/validation]

- **Depends on:** DUR-1; existing fenced leases, process reconciliation, mutation
  receipts, publication intents, lookup-before-create, and retry classification.
- **Remaining work:** Audit process launch, checkpoint, integration, commit, push,
  issue, and PR boundaries. Extend durable intent/result/reconciliation coverage
  only where missing. Add a fault-injection matrix for untested boundaries,
  especially sleep/wake, clock changes, disk errors, provider disconnect, and
  successful remote action followed by a lost response. Reconcile uncertainty
  before retry; reject late results from superseded attempts. Retain existing
  bounded retry and unchanged-failure handling instead of rebuilding them.
- **Acceptance:** Each injected failure leaves one explainable recoverable state,
  no stale-worker acceptance, no lost approved work, and no duplicate publication
  after reconciliation. Ambiguous remote state pauses safely. No universal
  exactly-once guarantee; no retry without relevant evidence or a viable action.
- **Verification:** Extend existing recovery, quota-wait, stop-runners, publication
  retry, and native lost-create tests only for missing cases. Kill/restart real
  local processes with Git/SQLite fixtures; use controlled remote simulators.

## Project execution policy

### ISO-1 — Enforce the assigned project boundary [P0; hardening]

- **Depends on:** Existing OS sandbox bridge, execution profiles, path policy,
  protected metadata, private provider runtime, and check broker.
- **Remaining work:** Define a versioned effective capability manifest for source
  reads, writes, runtime dependencies, private state, network, sockets, and tools.
  Restrict project data to assigned worktree/read-only snapshot. Replace arbitrary
  external data `read_roots` and shared Git-directory exposure with task-local
  metadata or narrowly scoped host operations. Audit broad runtime grants for
  paths that can contain unrelated user data. Keep only necessary executable and
  library reads plus private task home/cache/temp state. Scope authentication to
  supported brokered operations or minimum private provider state; never expose
  host credentials/configuration or general home directories.
- **Acceptance:** Shells, tools, hooks, MCP processes, checks, helpers, and resumed
  sessions inherit restrictions. Agents cannot inspect sibling projects, other
  tasks, orchestration state, or host secrets; modify host files/policy; or reach
  unrelated sockets and control services. Planning/review source stays read-only.
  Publication remains host-authorized. Missing enforcement blocks execution;
  incompatible existing policies require correction without discarding artifacts.
- **Verification:** Extend current real-sandbox tests with external-read grants,
  shared Git metadata, hardlink/symlink traversal and races, inherited descriptors,
  child processes, socket/network bypass, and host-control access. Check both
  macOS/Linux and the ISO-2 provider matrix; retain successful existing tests.

# Under The Hood

## Context Engine

### LEARN-2 — Fixed-budget memory and bounded session refresh [P1; new]

- **Depends on:** LEARN-1, DUR-1, and existing context-pack limits/provenance.
- **Remaining work:** Keep recovery state, project lessons, and aggregate outcome
  statistics separate. Compile only applicable validated lessons into each role's
  context. Default to at most five complete lessons and 2,048 UTF-8 bytes including
  provenance references, within the existing total context allowance. Omit whole
  lessons that do not fit; never truncate their meaning or append raw trajectories.
  Rank by applicability, supporting evidence, freshness, and measured usefulness.
  Refresh long-running sessions from bounded state instead of stacking summaries.
- **Acceptance:** Mandatory instructions and acceptance criteria retain priority.
  No relevant memory means no memory injection. Every included/omitted lesson has
  a recorded reason. Historical record count never increases the configured pack
  cap; provider-added hidden context is disclosed as outside engine accounting.
- **Verification:** Compare packs with 10, 1,000, and 100,000 historical records;
  test UTF-8 boundaries, complete-item omission, stale facts, relevance, fixed
  allowance, retrieval latency, and no cross-project or raw-history leakage.

### EFF-2 — Targeted context and stronger evidence validity [P1; hardening]

- **Depends on:** ISO-1, DUR-1, EFF-1; existing repository index, bounded context
  packs, opt-in check reuse, and review deltas.
- **Remaining work:** Improve symbol/test/interface/dependency selection and add
  bounded on-demand retrieval with provenance and spend attribution. Use stable
  prompt prefixes where provider caching supports them. Extend reuse identity
  to declared toolchain/dependency/environment inputs that executable hashing
  cannot establish. Unknown or changing inputs require rerun. Retain prior
  blockers and mandatory review perspectives during delta review.
- **Acceptance:** Smaller context/reused evidence does not omit acceptance
  coverage or preserve passes after relevant input changes. New reads and session
  refresh remain task-budgeted; optional context never crowds out instructions.
- **Verification:** Extend current context/reuse/delta fixtures for missing
  dependency and environment changes; compare retrieval coverage and whole-task
  outcomes on a matched corpus before enabling an optimization.

## Decision Engine

### EFF-1 — Complete whole-task economic attribution [P1; hardening]

- **Depends on:** DUR-1 and existing attempt ledger, session deltas, admission
  reservations, task reports, and evaluation endpoint.
- **Remaining work:** Audit attribution across planning, coding, review, repair,
  submission, discovery, and new learning/evaluation jobs. Add missing stage and
  parent-operation links rather than a second ledger. Include abandoned work and
  failed routes. Preserve reported input/output/cache writes/cache hits, reported
  cost, estimates, and unknown values; add reasoning usage only if advertised.
  Keep background learning/evaluation spend visible separately and attributable
  to source tasks without counting it twice.
- **Acceptance:** Aggregates reconcile to individual attempts with no double count.
  Unknown spend retains uncertainty/reservations. Where live telemetry is absent,
  UI distinguishes admission estimates from enforceable runtime/token limits.
- **Verification:** Extend existing budget/usage tests with missing stage paths,
  background jobs, resumed cumulative counters, delayed usage, and incomplete
  telemetry. No model calls are needed to verify ledger arithmetic.

### EFF-3 — Measured optimization and comparative evaluation [P1; validation]

- **Depends on:** EFF-1, EFF-2, LEARN-2, ISO-2; existing evaluation corpus/report,
  opt-in adaptive routing, ordered chains, and bounded parallel units.
- **Remaining work:** Establish reproducible matched baseline/candidate runs using
  identical task revisions, acceptance criteria, budgets, provider/model/thinking,
  and relevant environments. Include failed/unfinished tasks and learning costs.
  Measure total tokens/cost per accepted task, regressions/reverts, elapsed time,
  recovery success, repeated failures, and operator intervention. Keep explicit
  provider order authoritative unless adaptive routing is enabled; any learned
  route stays inside approved providers, capabilities, and budgets.
- **Acceptance:** Enable new retrieval, reuse, memory, routing, or concurrency
  optimizations only with adequate comparable evidence of lower consumption and
  no acceptance/regression/intervention deterioration. Unknown telemetry cannot
  substantiate savings. Existing single-agent default and parallel-scope checks
  remain; do not add a second scheduler or assume more agents improve outcomes.
- **Verification:** Reuse deterministic evaluation eligibility tests; add matched
  live reports under explicit spending allowances. Compare named FOSS workflows
  such as OpenHands, Aider, Cline, LangGraph-based harnesses, and mini-SWE-agent
  with pinned versions and documented differences. No universal superiority claim.

## Go and web interfaces

### PLATFORM-1 — Acceptance traceability and portable evidence [P2; extension]

- **Depends on:** DUR-1, DUR-2, EFF-2, and existing publication-evidence gates.
- **Remaining work:** Link each acceptance criterion to exact checks, artifacts,
  review findings, and unresolved uncertainty. Export a provider-neutral bundle
  of diff/tree identity, source/plan/policy/environment identities, approvals,
  findings, and verification provenance. Keep secrets and private logs out.
  Version the format and reuse recovery/evidence records rather than duplicating
  them. Detect environment drift before accepting imported or reused evidence.
- **Acceptance:** A local consumer can inspect why a change passed and detect a
  modified artifact or mismatched environment. Export does not grant execution
  authority, turn unverifiable claims into passes, or expose another project.
- **Verification:** Round-trip fixtures; tamper with diff, checks, policy, approval,
  and dependency identities; verify redaction and explicit missing evidence.

### PLATFORM-2 — Explainable operator control and inspection [P2; extension]

- **Depends on:** DUR-1, EFF-1, LEARN-1, and existing Inbox, Run history, task
  actions, version preconditions, notifications, and feedback records.
- **Remaining work:** Fill gaps in blocker/recovery explanations, remaining budget,
  uncertain spend, lesson provenance, and next permitted action. Add scoped
  in-flight corrections that invalidate only affected work/evidence/approvals.
  Add read-only local CLI inspection using existing APIs. Connect confirmed
  post-delivery acceptance/reverts/regressions to lesson invalidation and measured
  outcomes; existing feedback capture and distinct outcome categories stay.
- **Acceptance:** Operator can diagnose and steer a task without transcript
  archaeology. Stale corrections are rejected; unaffected approved work remains.
  Publication/merge/acceptance/regression remain separate observations, with unknown
  outcomes visible. Ordinary progress does not require repeated approvals.
- **Verification:** Extend existing browser/API decision, feedback, pause, and
  notification scenarios for steering, stale versions, uncertainty, and lesson
  rollback; verify CLI output against API records.

### API-1 — Additive contracts and migration compatibility [priority follows feature]

- **Depends on:** Each owning work item above; ships with that feature, not as a
  prerequisite for all implementation.
- **Remaining work:** Extend existing `/api/v1`, OpenAPI, ordered events, and
  versioned SQLite records for effective capabilities, recovery/reconciliation,
  lesson lifecycle, context composition, and evaluation provenance. Reuse existing
  task actions, records, and optimistic concurrency conventions before adding
  routes. Capability diagnostics must be read-only and redact authentication.
- **Acceptance:** Existing records remain readable; missing historical fields stay
  unknown. Upgrades preserve artifacts, budgets, pause state, scope, and approvals
  unless changed evidence requires explicit revalidation. Stricter isolation blocks
  incompatible execution instead of silently granting access.
- **Verification:** Migration/rollback recovery fixtures, old-record decoding,
  stale-version/idempotency checks, OpenAPI consistency, event ordering/resumption,
  and affected API/browser contracts.

## Delivery and release verification

- Deliver ISO-1/ISO-3, validate ISO-2, then DUR-1/DUR-2/DUR-3. Implement LEARN-1
  and LEARN-2 next; complete EFF-1/EFF-2/EFF-3 before performance claims. P2 follows
  verified foundations. API-1 accompanies each change.
- First vertical slice: a scoped task checkpoints, its process dies, a different
  eligible provider resumes from artifacts, checks/review complete, and accepted
  outcome produces a quarantined lesson. Validate and promote that lesson into a
  later fixed-budget pack; do not bypass approval or publication gates.
- Extend existing tests only where coverage is missing. Use `go test -race ./...`,
  `go vet ./...`, existing Node bridge/launcher tests, frontend build, and affected
  Playwright scenarios for implementation batches. Run OS sandbox tests on supported
  hosts with dependencies; report nested-sandbox restrictions explicitly.
- Keep three evidence classes separate: deterministic fixtures, live provider
  compatibility, and budgeted live quality/consumption comparisons. A passing
  fixture is not live certification; read-only connection metadata is not inference
  or publication evidence. No live spending or external writes without authorization.
- Keep this backlog current: remove an item after its acceptance and verification
  gates pass, linking durable behavior/evidence from the appropriate existing docs.
  Implementation present with missing proof becomes a validation-only item.

## Experimental differentiators — not release commitments

All experiments depend on ISO-2, DUR-1/DUR-2, and EFF-3's matched measurement
protocol. They have explicit allowances, simple controls, and stop conditions;
none expands scope, permissions, context limits, or automatic merge authority.

### EXP-1 — Compile outcomes into bounded reusable rules [P2; experiment]

- **Depends on:** LEARN-1/LEARN-2 and the shared experimental gates.
- **Experiment:** Compare validated fixed-budget rules against no lesson injection,
  with identical tasks, prompts, and provider settings. Separately compare turning
  reproducible failures into diverse executable regression fixtures instead of
  injecting failure prose.
- **Acceptance:** Promote only a mechanism that reduces whole-task consumption
  without degrading acceptance or increasing regressions/intervention; include
  rule generation and validation cost. Otherwise retain the simpler control.
- **Verification:** Held-out task replay for deterministic behavior and separately
  budgeted live A/B runs; record overfitting, contradictions, and false promotions.

### EXP-2 — Compare tiny plans before funding implementation [P2; experiment]

- **Depends on:** EFF-1 and the shared experimental gates.
- **Experiment:** Compare one default implementation agent with bounded competing
  plans followed by one funded implementation. Count every proposal, judge call,
  and abandoned attempt against the shared task budget.
- **Acceptance:** Keep only if measured acceptance-adjusted cost or operator time
  improves without quality loss. No default swarm or unbounded speculation.
- **Verification:** Matched corpus report including selection mistakes and all spend.

### EXP-3 — Learn through aggregate outcomes without historical prose [P2; experiment]

- **Depends on:** LEARN-2, existing optional routing, and the shared experimental gates.
- **Experiment:** Shadow learned routing preferences using structured outcomes
  without historical lesson prose; compare with fixed chains and bounded memory.
- **Acceptance:** Enable only if sufficient comparable observations demonstrate
  improved accepted outcomes per token. If signal is weak, retain explicit chains.
- **Verification:** Budgeted matched evaluation with cold-start/sparse-data cases;
  confirm exploration cannot select unapproved providers or bypass route policy.
