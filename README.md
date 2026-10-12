# Go backend implementation

Khan now has local Go/SQLite server and HTTP-backed web controls. See [Go backend setup and policy](docs/go-backend.md) and [HTTP API](API.md). Start server with `go run ./cmd/khan`; start `web/` with `KHAN_BACKEND_URL=http://127.0.0.1:8090 npm run dev -- --hostname 127.0.0.1`.

The previous README below describes the removed Python implementation and is retained as historical context.

---

# Khan

Khan is a local multi-agent decision system for software engineering work.

It turns a broad prompt into a tracked, inspectable pipeline: Codex writes an
implementation brief, Codex and Cursor Agent build independently in isolated
worktrees, completed candidates are cross-reviewed, and Khan produces a decision
card with evidence and explicit adopt/reject commands. Khan never auto-adopts
changes into your checkout.

An optional local browser control center is available after installing the
pinned dependencies in `web/`; see [docs/web.md](docs/web.md). The Python CLI
and workers do not require Node.js.

## Why Khan Exists

AI coding work becomes hard to trust when it lives only in terminal scrollback.
Khan gives that work durable structure:

- SQLite-backed records for tasks, pipelines, sessions, duels, reviews, queues,
  artifacts, and adoption decisions.
- Pipeline-first `khan ask` flow for planning, parallel implementation,
  cross-review, and human approval.
- Shadow-mode `khan assess` for comparing no-change with one or more local
  candidate diffs before a human chooses whether anything should land.
- Provider-neutral sessions for Codex, Cursor Agent, and custom adapters.
- Manual adoption and rejection so generated changes enter your working tree
  only when you choose.
- Operator views through `khan inbox`, `khan show`, `khan get`, metrics, and a
  terminal UI.

## Status

Khan is early FOSS infrastructure. It is usable for local workflows today, but
the public API, CLI shape, and storage schema may still evolve.

Implemented today:

- Project registration and local git repository discovery.
- Pipeline mode, single-agent mode, and queue mode for `khan ask`.
- Task capsules with validation commands, allowed paths, protected paths, and
  conflict domains.
- Codex and Cursor Agent adapters.
- Provider duels, cross-reviews, reports, and decision cards.
- Manual `adopt` and `reject` workflows with dirty-destination checks.
- Durable queue worker, daemon supervisor, metrics, and TUI.

## Requirements

- Python 3.12 or newer is recommended.
- `git`
- `codex` for Codex-backed runs.
- `cursor-agent` for Cursor Agent-backed runs.

The test suite uses fake provider binaries, so Codex and Cursor Agent are not
required just to run tests.

## Installation

```bash
make setup
```

This creates `.venv`, installs dependencies from `requirements.txt`, links the
`khan` launcher into `~/.local/bin`, initializes state, and runs `khan doctor`.

Override paths when needed:

```bash
make setup PYTHON=python3.12 INSTALL_DIR="$HOME/.local/bin"
```

Run tests:

```bash
make test
```

## Container Package

Khan publishes semantic-versioned OCI images to GitHub Packages through GitHub
Container Registry:

```bash
docker pull ghcr.io/nirmalhk7/khan:0.1.0
docker run --rm ghcr.io/nirmalhk7/khan:0.1.0 --help
```

Release tags use semantic versioning with a leading `v`, for example
`v0.1.0`. The package tags omit the leading `v` and also publish major/minor
aliases such as `0.1`, `0`, and `latest`.

## Quick Start

Initialize Khan and check the environment:

```bash
khan init
khan doctor
```

Run the default multi-agent pipeline against the current repository:

```bash
khan ask . "Implement the feature and run the inferred checks."
```

Assess a local transition without generating code or changing the checkout:

```bash
khan assess . "Add feature." --verify "make test"
khan assess . "Add feature." --contract .khan/evidence.yaml --max-actions 20 --max-seconds 600
khan diff <assessment-id> --candidate working-tree
khan adopt <assessment-id> --candidate working-tree
khan outcome record <assessment-id> --candidate working-tree --status healthy
```

`assess` snapshots the no-change baseline and each named local candidate into
isolated worktrees. A deterministic planner reads `.khan/evidence.yaml`, rejects
out-of-scope paths before spending command budget, ranks relevant checks by
criticality and evidence-per-second, then records why and where it stopped.
Exact base+patch+command receipts are reused when safe. One explicitly requested
verifier may advise on unresolved evidence. It never writes to the source
checkout or auto-adopts.

When configured, Khan can run bounded repository-owned counterexample
generators after ordinary evidence remains incomplete. Delayed healthy/regressed
outcomes calibrate later check ordering, and similar past transitions are
retrieved by changed files, source symbols, and behavior obligations. Provider
models remain optional; these moat layers are local and deterministic.

Inspect actionable records:

```bash
khan inbox
khan show <pipeline-id>
```

Adopt or reject explicitly:

```bash
khan adopt <pipeline-id> --provider codex
khan reject <pipeline-id> --provider cursor-agent
```

Queue a pipeline for a worker:

```bash
khan ask . "Update the docs." --mode queue
khan queue work --once
```

Use the original one-agent Codex task loop:

```bash
khan ask . "Make the small targeted change." --mode single
```

## Common Commands

```bash
khan inbox                         # actionable decision cards
khan show <id>                     # human-readable evidence view
khan get pipelines                 # pipeline records
khan get assessments               # shadow assessment records
khan get runs                      # task-loop runs
khan get sessions                  # provider sessions
khan diff <id>                     # recorded workspace diff when available
khan metrics                       # orchestration metrics
khan tui                           # terminal operator UI
```

Advanced workflows:

```bash
khan duel run . "Try two implementations."
khan cross-review <duel-id>
khan session start cursor-agent . --prompt "Inspect this repo."
khan replay <run-id> --provider codex
khan relay . "Plan with Codex, build with Cursor." --preset "codex-plan cursor-build"
```

## Configuration

Khan writes configuration to `~/.khan/config.yaml` by default. Set `KHAN_HOME`
to use another state directory.

```bash
KHAN_HOME=/tmp/khan-state khan init
```

See:

- `docs/configuration.md` for config fields and project policy.
- `docs/cli.md` for command reference.
- `docs/operations.md` for day-to-day operations.
- `docs/agent-adapters.md` for custom provider adapters.
- `docs/overview.md` for architecture notes.

## Project Layout

```text
khan                  CLI launcher
src/khan_core/        orchestration, storage, adapters, CLI, TUI
src/khan/             compatibility package
tests/                unittest-based test suite with fake providers
docs/                 user and operator documentation
deploy/               launchd and systemd examples
```

## Development

Set up the local environment:

```bash
make setup
```

Run the full test suite before sending changes:

```bash
make test
```

Guidelines:

- Keep changes scoped and prefer existing patterns in `src/khan_core`.
- Add or update tests in `tests/test_foundation.py` for user-visible behavior,
  storage migrations, and orchestration changes.
- Do not silently adopt generated changes; Khan's product contract is explicit
  human approval.
- Preserve existing worktree changes unless you intentionally own them.

## Contributing

Issues, bug reports, design notes, and pull requests are welcome. Good reports
include:

- Khan version or commit.
- Operating system.
- Command run.
- Relevant config snippet with secrets removed.
- Expected behavior and actual behavior.
- `khan show <id>` or artifact paths when available.

Before submitting a pull request, run:

```bash
make test
```

## Security

Khan runs local agent processes that can edit files inside configured
workspaces. Review generated diffs before adoption, keep secrets out of prompts
and artifacts, and use project `protected_paths` for sensitive files.

If you find a security issue, do not include secrets or exploit details in a
public issue. Open a minimal report first so maintainers can coordinate a safe
disclosure path.

## License

No license file is currently included in this repository. Until a license is
added, the default copyright restrictions apply. Add a `LICENSE` file before
redistributing or packaging Khan as open source.
