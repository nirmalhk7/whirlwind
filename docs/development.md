# Development

Use the Go version in `go.mod`, Node 20.9+, Python 3, and Git.

```sh
python3 -m venv .venv
. .venv/bin/activate
make tools
make format
make -k check
```

`make fmt-check` checks Go imports, JavaScript, and Python without rewriting files.
`make architecture-check` enforces production import boundaries, including platform-specific source.
`make check` also runs Go race tests, Node tests, vet, and all command builds.
`make quality` requires Qlty. Native execution currently requires macOS and nono 0.81.0.
Live provider/integration tests require explicit opt-in; ordinary checks do not certify them.

Entrypoints live in `cmd`, shared contracts in `internal/model`, state in `internal/store`,
orchestration in `internal/workflow`, UI in `internal/dashboard`, and local commands in `internal/control`.
Settings and committed project policy have separate owners: `internal/settingsfile` and `internal/policy`.
Node launchers share `scripts/launcher-paths.mjs`; embedded prompts are listed in `prompts/agents/prompts.go`.

Current operation: [supervisor](local-supervisor.md), [workflow](workflow-modes.md), [sandbox](nono-setup.md).
Historical plans and acceptance records are provenance, not current behavior or standing authorization.
