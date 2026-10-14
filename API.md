# Whirlwind HTTP API

Go server owns `/api/v1`; Next.js forwards same-origin requests to loopback `WHIRLWIND_BACKEND_URL` (default `http://127.0.0.1:8090`). See [OpenAPI contract](docs/openapi.json).

## Contracts

JSON resources use `{id,version,data,updated_at}`. Lists return resource arrays. Versions start at 1. Mutations require `Idempotency-Key`; resource updates/actions additionally require `If-Match` containing current numeric version. Same key/request returns recorded response; reusing key for different input returns 409. Keys are server-global. Requests require JSON objects and are limited to 1 MiB. External writes reconcile their GitHub marker/branch before retrying; DB resource and event updates commit atomically. Mutation response receipts are stored after action completion, so clients must retain current resource versions when recovering interrupted mutations.

Control Host must be localhost/loopback; Origin, when present, must exactly match HTTP Host. Responses disable caching and include `nosniff` and `no-referrer`. Errors use `{error:{code,message,request_id}}`.

## Routes

| Method | Path | Input / behavior |
| --- | --- | --- |
| GET | `/health` | Backend liveness `{status:"ok"}` |
| GET | `/overview` | `{projects,providers,tasks,directions,inbox}` resource arrays |
| GET | `/projects`, `/providers`, `/tasks`, `/directions`, `/inbox` | Resource arrays; `project` filter for resources with `project_id` |
| GET | `/{kind}/{id}` | Resource, with numeric ETag |
| POST | `/projects` | `{name,path,webhook_url?}`; committed policy and authenticated gh required; 201 |
| POST | `/projects/{id}/policy` | Confirm current committed policy; If-Match required |
| POST | `/projects/{id}/ideation` | Queue manual ideation; 202 `{operation_id}` |
| POST | `/projects/{id}/embedding-evaluation` | `{fixtures:[{query,relevant:string[],criteria:string[]}]}`; configured embedding endpoint required |
| POST | `/providers` | `{id,name,kind:"acp",command:string[],capabilities:string[],cost?}`; 201 |
| PATCH | `/providers/{id}` | Full provider object; immutable kind; If-Match required |
| POST | `/directions/{id}/select` | Begin planner task; If-Match required; 202 task resource |
| POST | `/tasks/{id}/answers` | `{answers:{question_id:string}}`; If-Match required |
| POST | `/tasks/{id}/pause` | Stop active run, retain artifacts; If-Match required |
| POST | `/tasks/{id}/resume` | Resume paused task; explicit operator authorization; If-Match required |
| POST | `/tasks/{id}/approve` | Approve critical-risk PR publication; If-Match required |
| POST | `/questions/{id}/answers` | Native agent `{answers:{question_id:string}}`; If-Match required |
| POST | `/permissions/{id}/decisions` | `{decision:"allow"\|"deny"}`; If-Match required |
| PATCH | `/inbox/{id}` | Resolve notification; If-Match required |
| GET | `/settings` | Global settings resource |
| PATCH | `/settings` | `{global_slots,project_slots,reconciliation_seconds}`; If-Match required |
| GET | `/events` | SSE, query `after` or `Last-Event-ID`; defaults 0 |

Additional read-only kinds: `plans`, `reviews`, `findings`, `permissions`, `questions`, `metrics`, `outcomes`, `sessions`, `checkpoints`, `units`, `indexes`, `index_history`, `verification`, `operations`.

Tasks expose `stage`, `plan_version`, issue snapshot, acceptance criteria, questions, units, worktree/branch, score, review passes, risk, approval, failing checks, and PR link. Stage values: `planning`, `questions`, `plan_ready`, `coding`, `repair`, `review`, `approval`, `publishing`, `paused`, `pr_created`, `complete`.

SSE emits `event: update`, ordered numeric `id`, and JSON `{id,kind,resource_id,data,created_at}`. Cursor must be nonnegative safe integer. Stream emits heartbeat comments each idle second. Reconnect with last consumed ID. Events are retained; delivery is at least once across reconnects, so consumers deduplicate by ID.

## Public webhook listener

Separate optional listener exposes only `POST /webhooks/github`. Requires `X-Hub-Signature-256`, `X-GitHub-Delivery`, and `X-GitHub-Event`. HMAC uses `WHIRLWIND_WEBHOOK_SECRET`. Supported events: `issues` and `ping`. Valid issue events return 202; unregistered repository returns 404. Control routes are never registered on this listener.

## Errors

403: invalid Host/Origin or webhook signature. 404: unknown route/resource/repository. 409: stale version, reused mutation key, invalid task stage. 413: oversized request. 422: invalid JSON/configuration/action or failed onboarding. 428: missing mutation key or resource version. 503: Next.js cannot reach backend, or webhook storage temporarily unavailable.
