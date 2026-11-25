# Local supervisor and terminal dashboard

Whirlwind's human interface is a Go terminal application using `rivo/tview`. The supervisor owns SQLite state and runs independently from the terminal session. The dashboard connects through a private Unix-domain socket; it reads the existing database read-only when the supervisor is offline.

See [seeded terminal acceptance](seeded-terminal-acceptance.md) to verify human decisions through the real terminal.

## Start

```sh
./bin/whirlwind start
./bin/whirlwind dashboard
```

Use `status`, `restart`, and `stop` to manage the supervisor. `stop` sends a named local command; when the supervisor is unreachable, it only signals a process whose saved supervisor token matches. It never acts on a PID alone. `make run` starts the supervisor in the foreground. `make dev` opens the terminal dashboard. Browser/Next startup is retired.

The supervisor socket lives under a per-user private directory in `/tmp`; the socket is mode `0600`, the containing directory mode `0700`, and the path is derived from the absolute data directory. Requests are bounded JSON messages with correlation IDs. Inspection operations use project-scoped pages of at most 100 records and event cursors capped at 500. The protocol accepts named domain operations only, not arbitrary HTTP paths.

Human mutations include an idempotency key and expected resource version. Plan review also binds to the hash of the exact plan content shown in the approval form. The dashboard never mutates SQLite directly. Closing, redrawing, or reopening the terminal does not reset the supervisor, attempt deadline, retry counters, approvals, or pending questions.

From an approved or active idea, **Cancel planning** revokes that approval even if the Planner is still waiting for a slot. A later worker reply cannot write the cancelled plan into the plans directory. Use **Retry planning** to approve a fresh attempt. If Markdown already reached the plans directory, the supervisor resumes publication with the same file and attempt after interruption. Reopen the idea to inspect the completed plan.

## Project memory

Add `project_memory: true` to an external project policy, then use
`whirlwind policy preview` and `whirlwind policy approve` to enable memory
for that project. Memory is disabled by default. Use the registered project
ID and one role from `ideate`, `plan`, `code`, or `review`.

```sh
./bin/whirlwind memory remember PROJECT code ATTEMPT 'Keep the generated fixture in a temporary directory.'
./bin/whirlwind memory list PROJECT code
./bin/whirlwind memory clear PROJECT code
```

An operator can save one short note per completed attempt in the same
project and role. Whirlwind records the source attempt, plan revision,
operation, policy and checkout commit where available. SQLite keeps up to
12 notes per role. Agent input can receive up to three relevant notes
within a total 2 KiB advisory budget, using local word matching and recency.
The configured role provider receives any notes included in its input.
Choose notes suitable for that provider and keep credentials out of them.

Recall requires the exact approved opt-in policy. A new approval starts
fresh recall. Use `memory clear` to remove a role's saved notes even when
memory is disabled. Previously generated prompt evidence follows the
existing run-evidence retention.

## Existing state and legacy service cutover

The supervisor initializes the current schema directly in `~/.local/share/whirlwind/whirlwind.db`. There are no schema versions or data migrations. Reopening a current database preserves pending records. Opening an obsolete development layout deletes its database state and recreates the current schema. See [SQL persistence details](persistence.md) for the `projects`, `ideas`, and `plans` tables. Dashboard reads do not initialize, recover, or create a database.

If an older browser/HTTP instance owns the same state directory, `start` fails with an actionable message. Stop that owned instance explicitly with `./bin/whirlwind stop`, then run `start`. Process ownership uses the saved launcher token; unrelated listeners and processes are left alone.

The TUI can inspect saved SQLite state without nono. Native agents and nono-backed checks require macOS and nono 0.81.0. Agents also require the configured host ACP adapter. Linux supports [opt-in offline verification](support.md#verification-on-linux) alongside inspection. See [nono setup](nono-setup.md) for the native path.
