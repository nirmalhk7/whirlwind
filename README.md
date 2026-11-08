# whirlwind_

**ENGINEERING IN MOTION**

## Ideas in. Verified software out.

Autonomous software engineering, in continuous motion.

Whirlwind is an open-source, agent-agnostic engineering harness that turns goals into verified software through autonomous execution loops. Bring your agents. Whirlwind handles the engineering process.

Codex, Claude, Cursor, and other ACP agents do the work. Whirlwind coordinates planning, implementation, testing, review, and delivery; preserves durable state; verifies results; and decides what happens next. Human questions, permissions, approvals, and local delivery evidence stay visible in the terminal dashboard.

**Agent-agnostic · ACP-native · Durable execution · Evidence-driven**

Whirlwind's identity rests on velocity, control, and correctness: maximize verified engineering output per unit of time, cost, and compute.

## Run locally

Requirements: Go version specified in `go.mod` or newer, Git, and a local NVIDIA OpenShell gateway configured before agent execution. The terminal dashboard and supervisor can open without OpenShell; model work fails closed until the gateway and compatible Linux agent image are ready. Node.js 20.9+ is required by the launcher and OpenShell bridge.

```sh
go mod download
./bin/whirlwind start
./bin/whirlwind dashboard
```

`start` builds and starts the durable local supervisor. `dashboard` opens the Go/tview terminal interface and reconnects to saved supervisor state. The supervisor owns approvals, execution, deadlines, recovery counters, and local command/event IPC; closing the TUI leaves it running. You can open another dashboard later without replaying decisions.

```sh
./bin/whirlwind status
./bin/whirlwind restart
./bin/whirlwind stop
./bin/whirlwind doctor
```

The launcher resolves its checkout from the executable, so its absolute path or a symlink also works outside the checkout. Set `WHIRLWIND_ROOT` to select a checkout and `WHIRLWIND_DATA_DIR` to select a state directory; the default remains `~/.local/share/whirlwind/whirlwind.db`, preserving existing records and in-flight work. `WHIRLWIND_SETTINGS_FILE` selects the authoritative YAML configuration. Keep the checkout in place while the supervisor runs.

If a legacy browser/HTTP instance still owns the data directory, `start` stops with an ownership message. Run `./bin/whirlwind stop` to stop only its registered processes, then start the supervisor. Whirlwind never kills an unrelated process or silently replaces the running instance. `make run` runs the local supervisor in the foreground; `make dev` opens the TUI.

Add absolute local Git repository paths to `projects` in `settings.yaml` to onboard them for normal workflows:

```yaml
projects:
  - /absolute/path/to/repository
```

Valid repositories appear in Projects with green `OK`; invalid paths remain visible with red `ERR` and details in Inbox. Refresh preserves existing project choices and runtime state. Agents lists every configured role's ordered model choices, reasoning effort, and provider connection status: green `up`, red `down`, or a truthful `paused`/`unverified` state when no current check is available. Connection checks follow `workers.provider_status_check_seconds` and pause while runners are stopped.

The Go/tview TUI displays saved plans, questions, permission requests, attempts, review and verification evidence, and local completion data. Inbox occupies the right pane; focus it and press `a` to choose a direction for approval or `h` for direction history. Direction approval and plan approval are separate. Decisions carry expected record versions and idempotency keys over a user-private Unix socket; the supervisor rechecks them before saving. Whirlwind has no browser frontend or UI server.

See [local supervisor and terminal setup](docs/local-supervisor.md), [OpenShell setup](docs/openshell-setup.md), [project policy](docs/go-backend.md), and the [HTTP API contract](API.md) for integration and webhook interfaces.

## Engineering loops

- Onboard a local Git repository without replacing its files or existing state. Each ideation run handles one project and proposes Expand, Optimize, Harden, and Simplify directions, backed by current evidence and checked against durable proposal history. An exhausted novelty budget produces an incomplete result rather than fabricated directions.
- Select and approve a direction, then review and separately approve its detailed plan. Plans live at `~/.whirlwind/plans/<Repo Name>_<Timestamp>.md`. Approval freezes the requirements and version; later specification changes require another approval.
- Configure independent ordered ACP agent/model/thinking queues for Ideation, Planner, Coding, Reviewer, and Finalizer in YAML. Bundled Claude, Codex, and Cursor profiles still require compatible installed, authenticated runtimes. Unsupported selections produce errors rather than silent substitutions.
- Every agent runs inside NVIDIA OpenShell in its own Git worktree. Trusted annotations in `prompts/agents/*.md` select already authorized connections; inserted issue or repository text cannot grant access. General public web access does not authorize additional integrations.
- Coding and review repeat until the unrounded requirement score is strictly greater than 80% and every mandatory gate passes. Exactly 80% fails. Gates include the frozen plan checks, configured tests and build, and no new Qlty findings. Existing Qlty debt stays visible; failed repository tests cannot be grandfathered into acceptance.
- Questions and permissions pause the affected plan. Ordinary answers preserve recovery history; an answer that changes scope returns through planning and human approval. Each active agent attempt has a fixed five-minute deadline; human waiting time is outside that attempt.
- Finalization preserves the exact reviewed snapshot on the plan's local delivery branch. The dashboard shows its commit, diff, review score, unmet requirements, and verification evidence. Agents cannot push, open PRs, or merge; the human decides whether to publish.

SQLite retains execution records, fenced ownership, pending decisions, mutation receipts, ordered events, and proposal history. Recoverable failures use persisted restart budgets and the same role's fallback queue. Unknown legacy runtime ownership remains paused until it can be verified safely.

## Configuration and naming

- CLI executable: `bin/whirlwind`; backend-only executable: `go run ./cmd/whirlwind`.
- State directory: `~/.local/share/whirlwind`, overridden by `WHIRLWIND_DATA_DIR` or `--data`.
- Database: `whirlwind.db` within the state directory.
- Project policy: `.whirlwind.yml` (execution policy generated and committed during onboarding with required Qlty configuration); existing `.whirlwind.yaml` also supported. Existing app-generated empty starters upgrade through Finish execution setup; custom policies remain operator-managed.
- GitHub labels: `whirlwind:ready` and `whirlwind:work`.
- Optional webhook secret: `WHIRLWIND_WEBHOOK_SECRET`.

Whirlwind creates one current SQLite schema directly, without schema versions or upgrade migrations. Reopening a current database preserves its records. Databases from older development layouts are unsupported; stop the supervisor and archive the old state before starting fresh. See [local supervisor setup](docs/local-supervisor.md).

## Visual identity

| Color | Hex | Purpose |
| --- | --- | --- |
| Midnight | `#0B1220` | Workspace background |
| Ion teal | `#5EEAD4` | Primary actions and success |
| Velocity blue | `#38BDF8` | Capability and informational accents |
| Energy violet | `#A78BFA` | Evidence accents |
| Cloud white | `#F8FAFC` | Primary text |
| Signal amber | `#FBBF24` | Focus and attention |

## Verify

```sh
node --test scripts/*.test.mjs bridges/claude/*.test.mjs bridges/openshell/*.test.mjs bridges/sandbox/*.test.mjs
go test -race ./... -skip '^TestACPTerminalCannotWriteOutsideSandbox$'
go vet ./...
go build ./cmd/whirlwind ./cmd/whirlwind-dashboard
qlty smells --all
qlty check --all
```

The Go command above runs the controlled-fixture suite. Complete acceptance also requires the real OpenShell commands in [OpenShell setup](docs/openshell-setup.md), including the separately selected terminal filesystem-boundary test. Run those with the disposable gateway environment; do not export its gateway selection into controlled-fixture runs. Excluding or skipping a physical test does not establish isolation.

## Repository

- `cmd/whirlwind/`: durable local supervisor and command entry point.
- `cmd/whirlwind-dashboard/` and `internal/dashboard/`: Go/tview terminal interface.
- `internal/control/`: named command/event protocol over a private Unix socket.
- `internal/`: orchestration, ACP providers, persistence, Git, policy, and verification.
- `bridges/openshell/`: guest launch, controlled egress, and scoped MCP transport.
- `prompts/agents/`: trusted base prompts and connection annotations.

## Settings and recovery

`settings.yaml` remains the authoritative YAML file; `--settings` or `WHIRLWIND_SETTINGS_FILE` selects another path. The configuration contains workers, ACP providers, and independent ordered role queues. Explicitly configure each required role; an absent queue cannot borrow a different role's choices. Invalid settings block new dispatch while running attempts retain their recorded configuration.

A role choice selects its ACP provider, model, and supported thinking configuration. Effective settings and configuration version are recorded for each attempt. Missing installation, authentication, or compatible capabilities produce actionable errors. Metadata-only account/quota checks do not authorize native model inference.

Worker stop/resume settings, pending questions, approvals, deadlines, and restart counters persist outside the terminal session. Closing the dashboard leaves the supervisor running. Normal restart preserves an active attempt's original deadline. Replacements must reconcile old processes, sandboxes, and interrupted tool actions before obtaining write ownership; stale results are rejected.

A recoverable failure allows the initial launch plus three restarts of the same configured choice, with default backoff of 1, 2, and 4 seconds. Exhaustion advances through that role's queue. Exhausting the queue saves state and pauses for human retry or configuration repair. Review corrections have no fixed iteration cap.

[Workflow details](docs/workflow-modes.md) and [local supervisor setup](docs/local-supervisor.md) explain approvals, local delivery, and recovery.
