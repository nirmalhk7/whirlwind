# Local orchestration, accounting and execution boundaries

Whirlwind turns a selected objective into a reviewed change on one local machine. Selection authorizes planning; a ready-labeled external issue becomes a candidate and an `awaiting_selection` task. Selecting the current candidate revision records objective, scope, policy hash and source fingerprint atomically with its task binding. Legacy tasks need explicit adoption. Ordinary technical decisions remain with agents inside that scope.

## Setup before unattended inference

Configure finite allowances in `settings.yaml` under `workers.admission`, or a committed project `budget`. Task-specific budgets override project and default task limits. Discovery has its own `discovery_budget`. No production spending allowance is inferred from a subscription percentage.

| Field | Meaning |
| --- | --- |
| `max_attempts` | Whole-task attempt ceiling, including failures, reviews and retries |
| `max_runtime_seconds` | Whole-task active runtime ceiling, including deterministic verification; human input waits do not consume it |
| `verification_attempts`, `verification_runtime_seconds` | Capacity held back from coding for mandatory review |
| `max_tokens`, `attempt_tokens` | Optional reported-token ceiling and explicit per-attempt reservation |
| `max_cost`, `attempt_cost` | Optional reported-cost ceiling and explicit per-attempt reservation |

Set `providers[].quota_pool` to the same explicit alias for configurations sharing an account. Configure `workers.admission.pools.<alias>` with `min_remaining_percent`, `max_age_seconds`, and optional `max_concurrent`. `*` is an explicit fallback pool policy. A zero reserve explicitly disables percentage protection for that pool; it does not make unknown quota measurable. Positive protection requires fresh native quota observations. Missing settings block inference with a setup reason.

Reservations are transactional across local runners. Task budget changes retain consumed allowance. Unknown usage retains its reservation rather than refunding an invented zero. Interrupted attempts remain uncertain until reconciled. Live measurable usage or runtime exhaustion can cancel an attempt; without live usage, token/cost enforcement stops at the next admission boundary and an attempt may overrun its estimate. Other applications consuming the same account remain outside Whirlwind's control.

## Sandbox setup

`bridges/sandbox` pins Anthropic Sandbox Runtime and its lockfile. The local bridge uses OS restrictions on macOS/Linux without Docker. Missing runtime dependencies, unsupported hosts, or incompatible profiles fail closed. There is no unrestricted provider fallback.

Committed policy's `sandbox` contains explicit `read_roots`, `allowed_domains`, and environment variable names in `environment`. Configure provider authentication narrowly: specific credential files or necessary environment variables, plus documented provider destinations. The bridge isolates writable home/config/cache state; explicit supported credential files are copied into that private runtime state. Do not grant an entire home directory as a credential exception. Provider native restrictions remain additional controls.

Ideation/planning/review source stays read-only. Coding writes only inside approved worktree scope. Git metadata and policy remain protected. Trusted engine performs publication. Every attempt/session records effective profile identity, and incompatible profiles invalidate reuse.

Checks resolve committed IDs. Reviewers receive `run_check(check_id)` through native tool or MCP transport. The broker archives the exact reviewed Git revision into a disposable snapshot and returns bounded output, artifact IDs, exit status, revision and profile identity. `checks[].output_paths` declares writable build output locations; source mutation is incompatible. Runtime caches and temporary files use isolated state. Unknown check IDs and unsafe output paths are rejected.

## Waiting, recovery and communication

`waiting_quota` retains resume stage, plan version, policy, affected pool, reason and next metadata recheck. It releases runner slots and starts no inference while waiting. Reset time schedules a recheck; it is not evidence that capacity is available. Manual/project/global pause and changed selection prevent wake. Authentication and policy failures remain actionable boundaries rather than retry loops.

Each stage transition writes a durable Inbox notification in the same transaction as the task. Messages carry current state, completed work, next action, relevant usage uncertainty and evidence reference. Repeated unchanged failures share a notification group; occurrence history remains in events. The terminal dashboard reads these durable notifications from the supervisor.

Discovery collects changed repository/GitHub inputs, persisted failed-check diagnostics and confirmed policy evidence before inference. Unchanged scheduled input skips generation. A ranked shortlist may be empty; `max_candidates` bounds it from 1–12 (default 4). Newly generated candidates state expected benefit, rough effort and remaining uncertainty alongside canonical evidence/fingerprints and scope. `observed_defect` requires persisted failed-verification evidence; unverified hypotheses and prospective improvements are `opportunity` candidates with explicit uncertainty. Historical/imported candidates may lack these newer assessment fields. Dismiss/defer/reopen preserve lifecycle; deferred candidates must be reopened before selection. Explicit manual `fresh:true` exploration can reconsider unchanged inputs while preserving budget and duplicate checks. Candidate history and source excerpts are bounded rather than including the full project history in every prompt.

## Efficiency controls

`context_input_tokens` bounds engine-supplied context using a conservative, labeled UTF-8-byte estimate unless an exact tokenizer is available. Mandatory instructions and acceptance criteria stay intact; oversized required input fails visibly. Optional source excerpts are deduplicated and trimmed on Unicode boundaries; embedding vectors never enter prompts. Persisted context-pack identity includes source revision and policy. Hidden provider-added context is not covered by this bound.

Check reuse is opt-in per check: `reusable: true` plus positive `freshness_seconds`. A clean source tree, base, plan, policy, command, executable hash, environment and freshness must match completed verification. Shell commands and unknown freshness rerun. External dependencies cannot be established from the executable hash alone: mark checks involving undeclared toolchains, services or changing data non-reusable. Failed/interrupted verification cannot supply cached passes.

Repair reviews use a delta only when prior review and verification provenance match the same plan, base, policy and risk, and the reviewed commit is an ancestor. Previous findings remain present and unresolved blockers cannot disappear by omission. Every required review perspective still runs. Publication requires current evidence.

Implementation uses one agent by default. `parallel_units: true` with an explicit `run_timeout_seconds` per-attempt cap permits only dependency-ready units with known, nonoverlapping scopes and shared task admission. Delegation records retain objective, dependencies, scope and reservation ownership. Explicit provider chains keep their order. Measured routing is disabled unless `adaptive_routing: true`; outcome comparisons remain available through the evaluation report.

## Evaluation and release gates

`GET /api/v1/evaluation` reads the whole ledger and durable task/session outcomes without model calls. It groups attempts by provider/model/thinking/role/task class and includes failed routes, reviews, incomplete telemetry and interventions. PR creation, merge, verified evidence and user acceptance are separate. Missing acceptance/regression observations remain unknown.

`internal/evaluation/corpus.json` maps representative cases to deterministic fixtures. Run `go test -race ./...`, `go vet ./...`, and Node bridge/launcher tests. OS sandbox fixtures must run on a supported host with sandbox dependencies; an outer sandbox may prevent nested sandbox execution.

Capture a report before enabling optional reuse, parallelism or routing changes. Compare the same versioned corpus with required acceptance coverage, complete token telemetry, verification outcomes, interventions and post-merge observations. This fixture suite makes no inference calls. Run live model comparisons only after supplying an explicit evaluation budget; live consumption and quality comparisons have not been measured by these fixtures. No percentage savings are claimed from smaller prompts alone.

Existing SQLite records remain readable. Missing historical telemetry stays unknown. Missing historical runtime blocks admission until explicitly reconciled with observed runtime; confirming termination alone retains unknown runtime and spend reservations. Upgrade does not adopt legacy work, restart manually paused tasks, loosen policy, or authorize merge.
