# Whirlwind HTTP API

The Go backend owns `/api/v1` compatibility and integration handlers. The human interface is `./bin/whirlwind dashboard`, using the supervisor's private Unix command/event interface. There is no browser frontend or Next.js proxy. See [local supervisor setup](docs/local-supervisor.md) and the [OpenAPI contract](docs/openapi.json).

Historical task/publication terminology below does not authorize remote publication or alternate model transports. The current local workflow requires explicit plan approval, ACP in OpenShell, a requirement score strictly greater than 80%, every mandatory verification gate, and local delivery only; see [workflow details](docs/workflow-modes.md).

## Contracts

JSON resources use `{id,version,data,updated_at}`. Lists return resource arrays. Versions start at 1. Mutations require `Idempotency-Key`; resource updates/actions additionally require `If-Match` containing current numeric version. Same key/request returns recorded response; reusing key for different input returns 409. Keys are server-global. Requests require JSON objects and are limited to 1 MiB. External writes reconcile their GitHub marker/branch before retrying; DB resource and event updates commit atomically. Mutation response receipts are stored after action completion, so clients must retain current resource versions when recovering interrupted mutations.

Persisted resource envelopes also include `created_order`, an original insertion position shared across resource kinds. Inbox uses this value to keep messages and idea batches in creation order; changes to `updated_at` never move existing entries.

Control Host must be localhost/loopback; Origin, when present, must exactly match HTTP Host. Responses disable caching and include `nosniff` and `no-referrer`. Errors use `{error:{code,message,request_id}}`.

## Routes

| Method | Path | Input / behavior |
| --- | --- | --- |
| GET | `/health` | Backend liveness `{status:"ok"}` |
| GET | `/directories` | Local folder browser `{path,parent,entries:[{name,path}]}`; optional absolute `path` query, defaults to home; folders only, hidden entries omitted |
| GET | `/overview` | `{projects,providers,tasks,directions,inbox}` resource arrays |
| GET | `/projects`, `/providers`, `/tasks`, `/directions`, `/inbox` | Resource arrays; `project` filter for resources with `project_id` |
| GET | `/{kind}/{id}` | Resource, with numeric ETag |
| POST | `/projects` | `{name,path,webhook_url?}`; preflight installed/authenticated gh before writes; initialize missing Qlty/CodeGraph and validate Qlty; generate and commit executable `.whirlwind.yml` plus missing Qlty configuration when no policy exists; checks come from Qlty and committed repository scripts; exact app-generated empty starters upgrade; custom policies remain unchanged; setup or commit failure returns 422 |
| DELETE | `/projects/{id}` | Unregister only; preserves repository files and task/run history; If-Match required; 200 `{id,deleted:true}`; 409 while tasks or ideation are active |
| POST | `/projects/{id}/policy` | Upgrade an unchanged app-generated empty starter to committed execution policy, or confirm current custom committed policy; If-Match required |
| POST | `/projects/{id}/pause` | Persist `paused:true` and cancel this project's active agents, task steps, and queued ideation, including capacity/retry waits; block new execution and scheduled/manual ideation across restarts; current If-Match required; 200 updated project resource |
| POST | `/projects/{id}/resume` | Persist `paused:false`; allow execution from saved checkpoints; individually paused tasks and global runner stop remain; current If-Match required; 200 updated project resource |
| POST | `/projects/{id}/ideation` | Queue manual ideation; 202 `{operation_id}`; 409 `project_paused` while project activity is paused |
| POST | `/projects/{id}/embedding-evaluation` | `{fixtures:[{query,relevant:string[],criteria:string[]}]}`; configured embedding endpoint required |
| POST | `/providers` | `{id,name,kind:"acp",command:string[],capabilities:string[],cost?}`; 201 |
| PATCH | `/providers/{id}` | Full provider object; immutable kind; If-Match required |
| POST | `/providers/{id}/test` | Queue read-only “Hello World” connectivity test; If-Match required; 202 `{operation_id}` |
| POST | `/providers/{id}/models` | Load advertised model IDs over ACP without inference; If-Match required; 202 `{operation_id}` |
| GET | `/agents`, `/agents/{role}` | Agent configurations for `ideate`, `plan`, `code`, `review`; unconfigured roles have version 0 |
| PATCH | `/agents/{role}` | `{chain:[{provider,model,thinking?:{config_id,value}},...]}` in strict primary/fallback order; If-Match required (0 for first save) |
| POST | `/directions/{id}/select` | Select the sole idea in its `group_id`, pausing the previous choice and beginning or restoring its planner task; If-Match required; 202 task resource |
| POST | `/directions/{id}/deselect` | Unselect idea and pause linked unfinished task; If-Match required; 200 direction |
| POST | `/directions/{id}/reconsider` | `{project_id,reason}` for an unselected dismissed/rejected direction; current If-Match and Idempotency-Key required; records a one-use human reconsideration request without selecting work or approving a plan |
| DELETE | `/directions/{id}/group` | Delete the idea's entire generation group and pause linked unfinished tasks, preserving task history; `{versions:{idea_id:version,...}}` for every member and If-Match for the target required; 200 `{deleted:number}` |
| POST | `/tasks/{id}/answers` | `{answers:{question_id:string}}`; If-Match required |
| GET | `/runners/status` | `{processing:boolean}`; true while a session has status `running` and is unfinished, including ideation and provider tests |
| POST | `/runners/stop` | Cancel current task, ideation, and connectivity runners, including capacity waits; pause all unfinished tasks with resume state retained; 200 `{paused:number}`; Idempotency-Key required; sets `workers.stopped: true` in the startup settings file; blocks new runners and scheduled work across restarts until explicitly cleared |
| POST | `/tasks/{id}/pause` | Stop active run, retain artifacts; If-Match required |
| POST | `/tasks/{id}/resume` | Resume paused task; explicit operator authorization; If-Match required |
| POST | `/tasks/{id}/approve` | Approve critical-risk PR publication; If-Match required |
| POST | `/questions/{id}/answers` | Native agent `{answers:{question_id:string}}`; If-Match required |
| POST | `/permissions/{id}/decisions` | `{decision:"allow"\|"deny"}`; If-Match required |
| PATCH | `/inbox/{id}` | Resolve notification; If-Match required |
| POST | `/inbox/clear` | Resolve all unresolved notifications atomically; retains history and pending agent questions/permissions; 200 `{resolved:number}`; Idempotency-Key required |
| POST | `/runners/resume` | Set `workers.stopped: false` and restore tasks paused by global stop to their saved stages; manual pauses remain; 200 `{resumed:number}`; Idempotency-Key required |
| GET | `/settings/file` | Exact startup `{path,content,loaded_at}`; never rereads disk. `?view=file` explicitly reads current `{path,content,revision}` at the startup path |
| PATCH | `/settings/file` | `{content,revision}`; validate and preserve raw YAML, atomically save file/runtime changes/receipt; 409 for stale file revision; Idempotency-Key required. Startup snapshot remains unchanged |
| GET | `/settings` | Global settings resource |
| PATCH | `/settings` | `{global_slots,project_slots,reconciliation_seconds,ideation?:{enabled,time,timezone}}`; If-Match required (0 for first save) |
| GET | `/events` | SSE, query `after` or `Last-Event-ID`; defaults 0 |

Additional read-only kinds: `plans`, `reviews`, `findings`, `permissions`, `questions`, `metrics`, `outcomes`, `sessions`, `checkpoints`, `units`, `indexes`, `index_history`, `verification`, `operations`.

Read `/operations/{operation_id}` for connectivity results. Its `data` contains `kind:"provider_test"`, `provider`, `stage` (`running`, `complete`, or `failed`), `elapsed_ms`, and final `text`/`session_id` or `error`. Tests use configured provider command, temporary read-only workspace, existing login, and normal global capacity limits. Each test times out after one minute, including capacity wait. Provider availability and quota remain unchanged.

Connectivity tests accept optional `{model:"exact-model-id",thinking:{config_id,value}}`. Explicit thinking runs native Codex/Claude sources through the same maintained ACP adapter used for discovery; default thinking preserves native transport. Model discovery operations contain `kind:"provider_models"`, `provider`, `stage`, `models:[{id,name}]`, optional `current`, and `error` on failure. Discovery accepts optional `{model}` and selects it before returning model-dependent `thinking_options:[{config_id,name,current,options:[{id,name}]}]`. It sends no inference prompt, times out after 30 seconds, and uses normal process capacity.

Agents are logical roles; Providers are model sources. A configured agent uses only its saved provider/model pairs in exact order, overriding project provider ranking and outcome/cost preferences. Missing/unavailable, role-incompatible, or exhausted sources are skipped. Confirmed quota exhaustion or authentication failure advances to the next eligible pair; ordinary errors pause execution. Quota-blocked tasks enter durable waiting; authentication, policy and budget boundaries retain explicit setup or recovery reasons. Existing worktree/handoff is retained; switching sources starts a fresh session. Sessions and metrics record selected model; resumption also requires matching transport and thinking configuration. Explicit pool aliases share admission across provider configurations. Reset times trigger metadata rechecks rather than proving availability. Unconfigured roles retain existing project routing.

Tasks expose `stage`, `plan_version`, issue snapshot, acceptance criteria, questions, units, worktree/branch, score, review passes, risk, approval, failing checks, and PR link. Stage values: `planning`, `questions`, `plan_ready`, `coding`, `repair`, `review`, `approval`, `publishing`, `paused`, `pr_created`, `complete`.

SSE emits `event: update`, ordered numeric `id`, and JSON `{id,kind,resource_id,data,created_at}`. Cursor must be nonnegative safe integer. Stream emits heartbeat comments each idle second. Reconnect with last consumed ID. Events are retained; delivery is at least once across reconnects, so consumers deduplicate by ID.

## Public webhook listener

Separate optional listener exposes only `POST /webhooks/github`. Requires `X-Hub-Signature-256`, `X-GitHub-Delivery`, and `X-GitHub-Event`. HMAC uses `WHIRLWIND_WEBHOOK_SECRET`. Supported events: `issues` and `ping`. Valid issue events return 202; unregistered repository returns 404. Control routes are never registered on this listener.

## Errors

403: invalid Host/Origin or webhook signature. 404: unknown route/resource/repository. 409: stale version, reused mutation key, invalid task stage, or active project work. 413: oversized request. 422: invalid JSON/configuration/action or failed onboarding. 428: missing mutation key or resource version. 503: Next.js cannot reach backend, or webhook storage temporarily unavailable.

Generated directions include `category`: `expand`, `optimize`, `harden`, or `simplify`, with exactly one per batch. Older directions may omit it. Ideation validates each batch against all stored suggestions for that project and requires a separate semantic novelty verdict before saving. Descriptions are capped at 35 whitespace-separated words. Rejected batches retry up to three times; failed validation or unavailable history/review leaves no new directions.

Empty project policies permit registration and ideation with an empty `policy_hash`. Task execution still requires a configured, committed policy confirmed through `/projects/{id}/policy`. Native providers accept optional `model_command:["executable", "argument", ...]` for ACP discovery; otherwise pinned official npm adapters are used.

## Thinking, Run history, and ideation schedules

Automatic issue execution requires an open issue tagged `whirlwind:ready`; `whirlwind:plan` alone does not authorize coding. Selected ideas run the planner first, then publish a ready issue with the specification, acceptance checklist, scoped implementation units, verification requirements, and structured plan before coding begins. Existing task checkpoints and publication markers retain their identity.

Newly generated execution policies run `qlty check --all --no-fix` and `qlty smells --all --include-tests --sarif` alongside repository scripts. Smell verification reads the SARIF results and blocks findings, invalid reports, or unsuccessful analysis even when the Qlty process exits zero. Existing custom project policies require an explicit update and confirmation to adopt these checks.

Each primary/fallback choice can persist optional `thinking:{config_id,value}`. Values come from ACP `thought_level` selectors after model selection, never a hard-coded effort list. Execution and Test model validate advertised support and apply the exact config ID/value before prompting. Unsupported or unconfirmed settings fail rather than silently using another level. Changing settings does not alter an active attempt. Unconfigured/native-default choices keep their existing transport.

`GET /sessions` includes each attempted provider separately, including ideation and novelty checks. New records add `project_id`, `provider_name`, `transport`, `thinking`, `effective_thinking`, `resolved_model`, `started_at`, `finished_at`, `elapsed_ms`, `success`, and failure `error` when available. Existing `model` remains the configured value. Runtime metadata is separate so it cannot change session identity. Historical absent fields remain unknown; `finished` alone is not proof of success. The Run page at `/runs` consumes these read-only records; connectivity tests remain operations.

Settings defaults merge into legacy records without migration. `ideation` defaults to `{enabled:true,time:"10:00",timezone:"America/Denver"}`. `time` requires 24-hour HH:MM; `timezone` requires an explicit IANA zone. Automatic ideation runs once per project/latest due local date, catches up only that date after downtime, honors existing completed schedules and retry delays, and stops scheduling new runs when disabled. Already-running work continues. Manual ideation remains available and uses the same due-day calculation. Schedule changes apply on subsequent scheduler checks without restart. Missing fields in PATCH preserve existing settings.

Project onboarding preserves custom `.whirlwind.yml`/`.whirlwind.yaml` policies, including committed/staged policies missing from the checkout. Exact unchanged app-generated empty starters upgrade to execution policies. Automatic commits include generated `.whirlwind.yml` and missing `.qlty/qlty.toml`, preserving unrelated staged/unstaged changes. Registration is blocked if Git cannot commit; existing Git hooks and signing settings are respected. Qlty must be installed for validation; missing configuration is initialized, invalid existing configuration remains intact for repair.

## Durable run evidence

`GET /sessions/{id}/events?after=0&limit=100` returns `{items,next_after}`.
Each item contains `sequence`, `attempt_id`, `kind`, `created_at`, `replay`, compact
`metadata`, and an artifact reference. `after` is a non-negative safe integer;
`limit` defaults to 100 and must be 1–500. Missing sessions/artifacts return 404;
invalid cursors/limits return 422.

`GET /sessions/{id}/evidence` returns `{items}` of `{kind,record}` for the latest
100 linked check, review, checkpoint, question, permission, audit, and quota
records. Reviewer inspection includes the verification referenced by its review.
`GET /artifacts/{id}` returns metadata without loading content;
`GET /artifacts/{id}/content` returns retained plain text with
`X-Evidence-Truncated`. Content is fetched lazily by Run inspection.

New session fields include `attempt_id`, `unit_id`, `operation_id`, `purpose`,
`previous_attempt_id`, `owner`, `status`, `policy_hash`, input/output artifact
references, requested native session ID, provider-confirmed `resumed`, and scoped
`usage`. `interrupted` leaves success unknown. Operation records retain kind,
subject, owner, input, start/finish time, and interruption reason. Connection tests
link their diagnostic attempt and bounded output artifact while remaining
inspectable through their original operation response.

Additional read-only resource kinds: `review_parts`, `audit`, `publications`,
`mutations`, `quota_observations`. Approval requires linked accepted evidence
matching the task's plan version, commit, base, and policy. Mutation changes and
replay receipts commit atomically; delayed version conflicts still return 409.

See [persistence and recovery](docs/persistence.md) for capture limits, credential
handling, restart reconciliation, and publication intent guarantees.

## Authoritative settings file

`GET /provider-status` returns `{providers,interval_seconds,stopped}`. Each provider contains `id`, `name`, `kind`, `status` (`working`, `failed`, `unavailable`, `paused`, or `unknown`), `detail`, and `checked_at`. Metadata-only connection checks are bounded to ten seconds (including capacity waits), cached across tabs for `workers.provider_status_check_seconds` (default 30; valid 1–86400), and invalidated by configuration/stop changes. Checks send no model prompts. `workers.stopped: true` prevents provider process launches; the response marks installed providers paused. A successful check confirms protocol readiness, not an inference result.

Root `settings.yaml` owns worker settings, provider definitions and ordered agent choices. Existing API shapes and numeric versions remain unchanged. UI POST/PATCH configuration mutations write YAML and cache state together; failed receipts restore the previous file. Valid external file edits refresh cache versions and emit normal events, so stale UI saves fail rather than overwrite those edits. Quota and availability observations remain runtime state and never rewrite YAML. Invalid YAML/schema/configuration returns an error instead of silently reverting to defaults. UI routes `/agents` and `/providers` redirect to `/settings#agents` and `/settings#providers`; API routes remain available.

## Bounded orchestration additions

All paths below use `/api/v1`. Mutations retain `Idempotency-Key` and current resource `If-Match`; stale versions return conflict.

| Method | Path | Behavior |
| --- | --- | --- |
| GET | `/tasks/{id}/usage` | Whole-task reported usage, unknown coverage, active/uncertain reservations and ledger entries |
| GET | `/tasks/{id}/budget` | Task override `budget` (nullable), resolved `effective_budget` (nullable), `budget_source` (`task`, `project_policy`, `default`, `unconfigured`), usage, enforcement limits and current task version |
| PATCH | `/tasks/{id}/budget` | Replace allowance without resetting previous consumption |
| POST | `/tasks/{id}/budget-reconcile` | Explicitly confirm interrupted child or legacy session termination; body `{attempt_id,confirmed_terminated:true,usage:null}` releases its concurrent slot while retaining unknown consumption reservations. Supplying attempt-scoped `usage` additionally requires observed `active_runtime_ms`; 200 updated task resource, 409 for unreconcilable reservation, 422 without termination confirmation |
| POST | `/tasks/{id}/adopt` | Explicitly authorize a legacy task objective before further execution |
| POST | `/tasks/{id}/outcome` | Append audited user feedback `{decision:"accepted"|"rejected"|"regression"|"reverted",reason?:string}`; 200 updated task version, execution stage unchanged; 422 invalid decision |
| POST | `/directions/{id}/dismiss` | Suppress candidate until relevant evidence changes |
| POST | `/directions/{id}/defer` | Retain candidate for later; explicitly reopen before selection |
| POST | `/directions/{id}/reopen` | Make dismissed/deferred candidate available again |
| GET | `/evaluation` | Read-only whole-run quality/usage report and route comparisons; no inference |

`POST /projects/{id}/ideation` accepts optional `goal`, `constraints`, `priorities`, `max_candidates` (1–12; omitted/0 uses configured schedule maximum, default 4), and `fresh`. Nonempty brief fields or a positive maximum save the brief for later runs; `{}` retains it. `fresh:true` bypasses unchanged-input skip for this operation while keeping budget and deduplication checks. Empty useful output succeeds. Selection atomically binds candidate version, scope, policy and source fingerprint to one task. Deferred/dismissed candidates return 409 until reopened. `awaiting_selection` never dispatches a planner; ordinary resume cannot adopt a legacy task. `/tasks/{id}/adopt` requires confirmed project policy and an unselected unpublished task, otherwise 409.

Task `wait` separates quota waiting from budget/setup/policy/manual boundaries. Native `quota_snapshot` retains windows, timestamps and explicit pool identity; missing values remain null/unknown. `workers.admission` configures default task and discovery budgets plus per-pool reserve/freshness. Finite attempt/runtime bounds are required; implementation also reserves verification capacity. Policy adds `sandbox`, `context_input_tokens`, `parallel_units`, `adaptive_routing`, and check output/reuse/freshness declarations.

Inbox notifications include `current`, `next_action`, `completed`, `evidence_id`, `usage`, occurrence count and first/last timestamps. Reconnect does not create a second durable alert. Full setup and enforcement limits: [local orchestration](docs/local-orchestration.md).

Generated directions include `rank`, `expected_benefit`, `effort_estimate`, `uncertainty`, and `problem_kind` (`observed_defect` or `opportunity`). An observed defect requires persisted failed-verification evidence. Source hypotheses and prospective improvements are opportunities and must state remaining uncertainty. These assessment fields may be absent on historical or imported issue candidates.

`GET /provider-status` reports optional `quota_pool` and `quota_snapshot` separately from protocol connectivity. Quota snapshots retain the complete native response in `raw_metadata`; `applicability` remains `unknown`, including historical snapshots, because provider model/account applicability is not inferred. Routing remains conservative across reported windows. Its `quota_fresh` is boolean or null; null means native quota freshness is unknown. No token-to-subscription-percentage conversion is performed.

Usage reports preserve missing historical runtime in `unknown_runtime_attempts`; admission blocks instead of treating it as zero. Entry `kind` distinguishes model attempts from synthetic `legacy` unknowns, deterministic `verification` checks, and `preflight` with no model turn. Reconciliation of legacy session IDs also requires confirmed termination; supplying usage requires attempt scope and observed `active_runtime_ms`.
