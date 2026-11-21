# Local supervisor and terminal dashboard

Whirlwind's human interface is a Go terminal application using `rivo/tview`. The supervisor owns SQLite state and runs independently from the terminal session. The dashboard connects through a private Unix-domain socket; it reads the existing database read-only when the supervisor is offline.

## Start

```sh
./bin/whirlwind start
./bin/whirlwind dashboard
```

Use `status`, `restart`, and `stop` to manage the supervisor. `stop` sends a named local command; when the supervisor is unreachable, it only signals a process whose saved supervisor token matches. It never acts on a PID alone. `make run` starts the supervisor in the foreground. `make dev` opens the terminal dashboard. Browser/Next startup is retired.

The supervisor socket lives under a per-user private directory in `/tmp`; the socket is mode `0600`, the containing directory mode `0700`, and the path is derived from the absolute data directory. Requests are bounded JSON messages with correlation IDs. Inspection operations use project-scoped pages of at most 100 records and event cursors capped at 500. The protocol accepts named domain operations only, not arbitrary HTTP paths.

Human mutations include an idempotency key and expected resource version. Plan review also binds to the hash of the exact plan content shown in the approval form. The dashboard never mutates SQLite directly. Closing, redrawing, or reopening the terminal does not reset the supervisor, attempt deadline, retry counters, approvals, or pending questions.

## Native task queues

The default supervisor starts a second private Unix socket for its HTTP task
queue API. It uses the same owned mode-`0700` directory and mode-`0600` socket
as the human control channel. It is not mounted on a TCP or webhook listener.
The pathname is the human control socket's pathname, replacing the
`.sock` suffix with `-queues.sock`. The slot names are:

| Queue | Committed eligibility |
| --- | --- |
| `planner_tasks` | Selected work, including an approved idea. A generated plan still requires separate approval. |
| `coding_tasks` | Approved plan, or correction from implementation review/required verification. |
| `reviewer_tasks` | Coded or repaired candidate. Reviewer checklist preparation runs in this role, not in the coding role. |
| `finalizer_tasks` | Accepted review above 80%, passed required checks, and exact-candidate human approval when critical. |

SQLite stages only the plan ID, version, role and delivery metadata in the
same transaction as the associated business state. The supervisor uses HTTP
push/pop to expose committed work and claim the eligible FIFO head. A live
lease keeps that queue's head in place. Paused projects are ineligible until
resumed. The supervisor rotates roles across admissions to avoid starvation
when slots are scarce. Expired deliveries recover with a fresh receipt;
completed transitions retire the old revision in the same transaction.
Workers recheck exact approvals, evidence, and the existing execution fence.
A failed worker can leave work retryable; this does not guarantee exactly-once
provider calls after a crash. Finalization remains local.

`workers.ideation.enabled` controls scheduled ideation. The default remains the
configured daily `time` in `timezone`. Set `workers.ideation.interval_minutes`
to 1–10080 for an elapsed-minute schedule instead. Each slot has its own durable
key, and a restart catches up only the latest slot.

See [role queue HTTP endpoints](../API.md#role-queue-http-endpoints) for
optional same-user inspection and claiming. Do not leave a manual pop leased
when the supervisor should continue processing it: release it, or let its
lease expire.

## Existing state and legacy service cutover

The supervisor initializes the current schema directly in `~/.local/share/whirlwind/whirlwind.db`. There are no schema versions or data migrations. Reopening a current database preserves pending records. Opening an obsolete development layout deletes its database state and recreates the current schema. See [SQL persistence details](persistence.md) for the `projects`, `ideas`, and `plans` tables. Dashboard reads do not initialize, recover, or create a database.

If an older browser/HTTP instance owns the same state directory, `start` fails with an actionable message. Stop that owned instance explicitly with `./bin/whirlwind stop`, then run `start`. Process ownership uses the saved launcher token; unrelated listeners and processes are left alone.

Missing nono does not prevent opening the TUI or inspecting SQLite. Agent execution remains blocked until nono 0.81.0 and the configured host ACP runtime are available. See [nono setup](nono-setup.md).
