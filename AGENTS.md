# Code placement

- `cmd/whirlwind` wires and runs the supervisor. `cmd/whirlwind-dashboard` starts the terminal client. Keep terminal views and interaction in `internal/dashboard`.
- `internal/control` owns the Unix socket protocol, command names at the transport boundary, and shared wire types. `internal/httpapi` owns HTTP handlers and dispatches named local commands through `ExecuteLocal`. The HTTP fallback there serves legacy direction commands.
- `internal/workflow` owns orchestration and state transitions. `internal/model` owns domain types shared by workflow, storage, and clients. `internal/store` owns SQLite queries, transactions, and durable records.
- `internal/settingsfile` owns YAML settings. `internal/policy` owns project policy parsing and scope. `internal/verification` owns check results and verification evidence.
- `internal/provider` owns ACP and provider behavior. `internal/execution` owns execution profiles and sandbox launch. `internal/process` owns subprocess lifecycle. `bridges/` contains runtime-specific adapters and `prompts/agents/` contains trusted prompt templates.

Run `make fmt-check architecture-check`, affected tests, and `make -k check` after source changes. Use `docs/development.md` to install the pinned tools.
