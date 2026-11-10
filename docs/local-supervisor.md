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

## Existing state and legacy service cutover

The supervisor initializes the current schema directly in `~/.local/share/whirlwind/whirlwind.db`. There are no schema versions or upgrade migrations. Reopening a current database preserves pending records; older development database layouts require a fresh start. Stop the supervisor and archive its state before resetting it. Dashboard reads do not initialize, recover, or create a database.

If an older browser/HTTP instance owns the same state directory, `start` fails with an actionable message. Stop that owned instance explicitly with `./bin/whirlwind stop`, then run `start`. Process ownership uses the saved launcher token; unrelated listeners and processes are left alone.

Missing nono does not prevent opening the TUI or inspecting SQLite. Agent execution remains blocked until nono 0.81.0 and the configured host ACP runtime are available. See [nono setup](nono-setup.md).
