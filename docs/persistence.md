# Durable execution evidence

Whirlwind keeps workflow aggregates in SQLite `resources`, with version-checked
writes and ordered SSE events. A single current schema creates `artifacts` and
`attempt_events` directly. Schema versions and old-database upgrades are not
supported. Reopening current state preserves saved payloads and events.

## Attempts and inspection

Every new session record has an `attempt_id` equal to its resource ID. Inputs are
captured before the provider starts: rendered prompt (including supplied context
and handoff), execution policy, policy hash, provider configuration, configured
model/thinking, read-only mode, and context commit/index version. Native session
IDs remain separate from attempt IDs. Unit, operation, predecessor, and review
perspective links distinguish parallel work, retries, and specialist reviews.

Open **Run**, then **Inspect run**. The inspector lazily loads ordered events and
artifact content. It includes visible messages, tools and replies, bounded
terminal output, provider stderr, process exit, stop reasons, quota observations,
configuration, and ACP replay markers. Linked checks, review assessments, combined
reviews, checkpoints, questions, permissions, and decisions show which evidence
belongs to an attempt. The linked-record panel shows the latest 100 records;
the event timeline supports cursor pagination.

Detailed content is stored separately from resource/SSE snapshots. Session
summaries and connection-test previews are compact; complete bounded output is
available through artifact references. Artifact metadata includes original byte
count, retained byte count, SHA-256, content type, owner kind, and truncation.

## Limits and data handling

- Detailed payload budget: **25 MiB per attempt**.
- Per prompt/message/tool payload or check output: **1 MiB**.
- Per reviewed patch: **5 MiB**, within its owning attempt's budget.
- Provider stderr: drained continuously; first **1 MiB** retained across chunks.
- Connection-test preview: **4 KiB**, with output artifact reference and truncation flag.

Truncation is explicit. Compact identifiers/outcomes remain available after the
payload budget is exhausted, and essential workflow state continues to persist.
These are payload limits, not a total database-size limit: metadata, existing
repository indexes, and retained attempts also consume space. No automatic
pruning or historical deletion runs.

Credential-bearing protocol fields, environment maps, marked secret content,
credential CLI flags, and private thinking blocks are excluded from captured
structured evidence. Secret native questions are rejected; authentication stays
with the installed provider login/project environment. Unknown provider usage or
cost remains unknown. Codex session-cumulative token usage is labeled as such
and is not counted as attempt usage; Claude reports attempt usage separately.

## Recovery and atomicity

Database-only HTTP mutations commit resource changes, audit records, ordered
events, and replay receipts together. Asynchronous work starts only after its
operation and `202` receipt commit. Onboarding and embedding evaluation journal
synchronous external intent before execution. No agent or external command runs
inside a database transaction.

Attempt completion and metrics, unit progress and checkpoints, review/findings
and task transition, PR publication/task/notification, and final PR observations
commit as related bundles. Required evidence failures stop advancement, including
when a provider catches a tool callback error or also reports quota exhaustion.

At startup, after the exclusive server lock and child-process reaping, abandoned
attempts become `interrupted` with unknown success. Running operations fail with
`failure_kind: "interrupted"`; orphaned native questions/permissions are cancelled.
Answered questions, planner answers, worktrees, and checkpoints remain. Retained
native answers enter future attempt prompts as clarification, never authorization.
Old leases
expire while retaining fencing counters. Task dispatch resumes under fresh leases;
connectivity/model-discovery operations require a new explicit request.

Issue/PR publication journals exact request content, repository, plan version,
branch/base/head, policy, request fingerprint, owner, and result/error before
GitHub writes. Uncertain outcomes reconcile against existing issue markers/PR
branches, with strict content/head/base matching. Mismatches pause work rather
than accepting another remote object. Git remains the source of recoverable
repository history; bounded patches preserve inspection evidence after worktree
removal.

Checks persist incrementally with command, effective timeout, timing, exit result,
output reference, and truncation. Verification and reviews bind to base/head,
policy, plan version, and producing attempts. Human publication approval binds to
accepted review/verification evidence. Missing or stale evidence requires fresh
verification/review and, where applicable, approval.

## Read API

All endpoints use existing loopback/origin and no-cache protections.

- `GET /api/v1/sessions/{id}/events?after=0&limit=100`: `{items,next_after}`;
  `after` is a non-negative safe integer sequence, `limit` is 1–500.
- `GET /api/v1/sessions/{id}/evidence`: `{items}` containing `{kind,record}`
  for up to 100 latest linked evidence records.
- `GET /api/v1/artifacts/{id}`: metadata only.
- `GET /api/v1/artifacts/{id}/content`: retained bytes as plain text, with
  `X-Evidence-Truncated: true|false`.
- Existing resource reads also expose `review_parts`, `audit`, `publications`,
  `mutations`, and `quota_observations`.

Existing `/sessions`, `/operations`, mutation status codes, and replay behavior
remain compatible. New fields are additive; historical absent values stay unknown.
