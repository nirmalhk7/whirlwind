# Project and role learning

Ideation, Planner, Coding, and Reviewer keep separate experience memories for each
onboarded repository. Memories belong to Whirlwind's SQLite state, so changing an
agent's provider or model does not discard them. Existing projects start empty.
Unregistering a project preserves history and lessons; re-onboarding the same
repository restores its project identity and memory.

## Recording and recalling experience

The shared prompt appends `prompts/agents/learning.md`. Each role returns its normal
result inside `{result,learning:{lessons,feedback}}`. The workflow unwraps the result
before the usual role validation. Legacy results remain accepted. Original provider
output remains available as attempt evidence.

Learning is optional: an agent records a short lesson after a meaningful correction,
not after every tool call. No extra model call or agent is launched. Ideation stays
read-only and performs no additional command execution for learning. Connectivity
and model-discovery operations do not participate.

Each lesson describes `trigger`, `mistake`, and `correction`, with optional relevant
`paths`, `symbols`, `error_signature`, and `evidence_ids`. At most two lessons are
accepted per report; each has at most 600 UTF-8 bytes of descriptive text and eight
paths, symbols, or artifact references. The harness assigns project, role, producing
attempt, and provider metadata. Source files must be accessible within the repository;
escaping symlinks, traversal, `.git` files, nonregular files, and files larger than
1 MiB are rejected. Source contents are fingerprinted rather than copied into lessons.
Explicitly secret lessons/feedback and common credential-bearing text are rejected.
Agents must never submit secrets; text-pattern screening is not a general secret detector.

Before a normal role operation, SQLite FTS5 ranks lesson prose using weighted BM25.
Project/role/provider metadata never becomes search prose. Indexed paths, symbols,
and error signatures outrank prose matches, including identifiers outside the top
128 prose candidates. Initial queries use issue and handoff facts, excluding static
role instructions. Source fingerprints are checked before delivery; changed advice
becomes stale. Only three lessons fit the automatic block, capped at 2 KiB UTF-8.

Each role can call read-only `search_lessons` when an unexpected problem appears.
The official MCP Go SDK exposes this tool to ACP and Claude; native Codex uses a
scoped dynamic tool. No global provider configuration is edited. The bridge binds
loopback with an ephemeral bearer token, exposes one search operation, and closes
with the attempt. Older native Codex versions rejecting dynamic tools continue with
automatic recall and an inspection warning.

Allowances persist under project/task/plan/role/unit/purpose/operation/stage scope:
2 KiB total automatic memory plus two searches of at most 2 KiB each. Retries and
session rotations do not reset them. Already delivered lesson versions are excluded.
The inspector shows automatic bytes, search queries, match reasons, delivered
versions, and search allowance. Bytes/4 is a local estimate, not provider usage.
Static learning/tool instructions are additional input. Provider conversation history
can still grow; the memory cap does not erase native history.

### Optional semantic search

FTS5 works without a model download or embedding service. Semantic retrieval is
opt-in through committed policy: `embeddings.lessons_enabled: true`, with an
explicit `endpoint`, `model`, and optional `key_env`. It uses the existing
OpenAI-compatible embeddings request shape. Lesson text and queries are sent to
that configured endpoint. No local model is installed.

Submit 1–100 labeled fixtures to
`POST /api/v1/projects/{id}/lesson-search-evaluation`, using current project
`If-Match`, a fresh `Idempotency-Key`, and
`{"fixtures":[{"role":"code","query":"retry writes safely","relevant":["lesson-id"]}]}`.
Labels must reference active, current lessons from the same project/role. Semantic
ranking activates only when recall@3 improves, precision@3 does not regress, and
exact-match coverage is preserved. Each endpoint/model/key-environment configuration
needs its own passing evaluation. Evaluation errors do not silently enable it.

Vectors are cached by project, lesson version, and configuration; query vectors are
cached by query hash and configuration. Missing lesson vectors use batches of 32.
Cosine matches (at least 0.75) combine with BM25 through reciprocal rank fusion;
exact identifiers retain priority and prompt limits stay unchanged. Endpoint failures
fall back to text search with an inspection warning. The candidate window is bounded
before source checks, so stale high-ranked candidates can temporarily hide lower
semantic matches until those stale records are invalidated.

### Context, recovery, and output contracts

ACP tool-call, plan, and usage updates and native provider events normalize into
attempt evidence. Context occupancy uses a reported current window, separate from
cumulative tokens/cost. ACP cost evidence preserves amount, currency, and session
scope; only reported USD enters dollar-denominated usage fields. Missing reports remain unknown. When occupancy reaches
85%, the next resumed turn starts fresh from a durable bounded checkpoint. Each role
scope permits three context rotations. Provider compaction may lower reported usage;
Whirlwind uses the latest valid report rather than adding occupancy samples.

Three identical failed actions with unchanged source evidence request graceful
cancellation, then restart with a checkpoint and instruction to change approach.
Successful reads alone never count as spinning. Five unchanged verification failures
use the same recovery allowance. Across roles, a task/plan permits two automatic
recovery restarts; exhaustion pauses for user steering. Ideation scopes this allowance
to a generation, shared with its novelty check, so later generations start independently. In-flight actions with uncertain
completion pause instead of replaying. Checkpoints retain objective, worktree,
branch, evidence references, and bounded failing checks. No summarization model call
is added and worktrees are preserved.

Version-one JSON Schema contracts validate direction, plan, review, and novelty
outputs alongside existing semantic checks. Invalid output receives at most one
read-only response repair; Ideation retains its three-batch ceiling. Invalid or
interrupted primary output cannot teach lessons. Malformed optional learning metadata
is ignored with a warning while a valid primary result remains usable.

## Correcting lessons

New observations are `tentative` and can be recalled automatically as advisory
guidance. Repeating the same lesson does not establish its correctness. Selecting
another direction does not count as rejecting an idea, and a transient provider
failure does not establish that an engineering approach is wrong.

Normal role output can submit up to three feedback entries:

```json
{"lesson_id":"recalled-id","verdict":"supports","reason":"Confirmed the caller guarantee","paths":["src/caller.go"]}
```

Verdicts are `supports`, `contradicts`, or `supersedes`. Feedback applies only to a
lesson recalled at its current version for the same project and role. The agent
can cite source paths it observed or additional check artifacts. The harness captures
source-path fingerprints as bounded evidence; artifact references must belong to
the same project and current task and retain complete content. Original lesson
artifacts, assistant replies, prompts, and lifecycle events cannot corroborate a
lesson. Source-based corroboration represents another observed reading, not a proof
of the model's causal explanation. All lessons remain advisory.

Supporting feedback marks a lesson `corroborated`; contradiction or replacement
retires it. Changed or missing source fingerprints mark it `stale` before recall.
Fresh observations can refresh stale lessons. Retired lessons never revive through
duplicate submissions. Evidence-only observations remain historical and tentative
unless corroborated; their relevance is matched to the current task rather than
inferred from a source fingerprint.

Lesson changes, audit records, and per-attempt learning receipts commit together.
Concurrent/replayed reports do not duplicate effects. Recall or report errors are
visible as learning warnings without invalidating an otherwise valid role result.
Required attempt/evidence persistence retains its normal failure behavior. Partial
output from failed or interrupted provider calls does not create lessons.

## Inspection and API

Open **Run → Inspect run → Learning** to see the recalled IDs and versions, new
lessons, feedback, warnings, and memory size. Lesson details show their current
status separately from the status originally recalled. **Retire lesson** uses the
current resource version and the normal idempotent mutation contract.

- `GET /api/v1/lessons?project_id=...&role=code&status=tentative&after=0&limit=100`
  returns `{items,next_after}`. Project is required; role/status are optional.
  Pagination uses stable creation order; limits are 1–500.
- `GET /api/v1/lessons/{id}` returns the versioned lesson resource.
- `POST /api/v1/lessons/{id}/retire` requires `If-Match`, `Idempotency-Key`, and `{}`.
  Stale versions return 409; identical requests replay the saved response.

There is no public lesson-creation API or automatic sharing across roles/projects.
The existing listener/origin protections apply. Lesson retirement is auditable;
historical attempts retain their recalled versions.

## Optional evaluation

The ordinary tests use disposable repositories and fixture providers. An opt-in test
performs paired authenticated role probes with an empty memory and a teaching turn:

```sh
WHIRLWIND_LEARNING_EVAL=1 go test ./internal/workflow -run '^TestLearningEvaluation$' -v -count=1
```

Default transport: native Codex. Optional variables:
`WHIRLWIND_LEARNING_EVAL_KIND` (`codex`, `claude`, `acp`),
`WHIRLWIND_LEARNING_EVAL_COMMAND` (JSON command array), and
`WHIRLWIND_LEARNING_EVAL_MODEL`. The command must be installed and authenticated.
The probes request no repository changes, command execution, or external publication.
They consume model usage only when explicitly enabled.

JSON log rows include role, acceptance, attempts, elapsed time, recalled bytes, and
reported usage, including the teaching turn's overhead. Unknown or session-cumulative
usage is never invented or summed as per-attempt usage. These controlled probes do
not measure full software-delivery quality or establish token savings. Repeat with
matched models/tasks and evaluate real accepted changes before claiming an advantage.
