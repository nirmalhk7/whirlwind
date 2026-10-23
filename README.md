# whirlwind_

**ENGINEERING IN MOTION**

## Ideas in. Verified software out.

Autonomous software engineering, in continuous motion.

Whirlwind is an open-source, agent-agnostic engineering harness that turns goals into verified software through autonomous execution loops. Bring your agents. Whirlwind handles the engineering process.

Codex, Claude, Cursor, and other ACP agents do the work. Whirlwind coordinates planning, implementation, testing, review, and delivery; preserves durable state; verifies results; and decides what happens next. Human questions, permissions, and critical publication decisions stay visible in Inbox.

**Agent-agnostic · ACP-native · Durable execution · Evidence-driven**

Whirlwind's identity rests on velocity, control, and correctness: maximize verified engineering output per unit of time, cost, and compute.

## Run locally

Requirements: Go version specified in `go.mod` or newer, Node.js 20.9+/npm, Git, and authenticated GitHub CLI. Install and authenticate your preferred agents separately.

```sh
go mod download
npm ci --prefix web
make dev
```

Open `http://127.0.0.1:9876`. The combined launcher runs Go on `127.0.0.1:9877` and wires the Next.js proxy automatically. Ctrl-C stops both processes; either process exiting stops the other. Occupied ports fail startup.

Override ports with `make dev WHIRLWIND_LISTEN=127.0.0.1:8091 WHIRLWIND_WEB_PORT=3001`. `make run` and `make web` start services separately. `WHIRLWIND_BACKEND_URL` overrides the UI proxy. Install the optional Claude SDK bridge with `npm ci --prefix bridges/claude`.

### CLI from any directory

Add this checkout's `bin` directory to your `PATH`, or symlink `bin/whirlwind` into a directory already on `PATH`. The executable resolves its checkout even through a symlink; no installation or shell alias required. Keep the checkout in place.

```sh
whirlwind start
whirlwind status
whirlwind restart
whirlwind stop
whirlwind doctor
```

`start` rebuilds the Go backend and compiles the UI with `next build`, then launches the backend and `next start` in the background. The production UI has no development compiler or file watchers; Node.js remains required for the Next.js API proxy and server routes. Production builds use `web/.next-production` by default, separate from `make dev` output. Use `make dev` for hot reload. Startup allows up to five minutes for builds and returns after both HTTP services are healthy. Repeated `start` keeps an already healthy instance. `restart` rebuilds and restarts both services; `stop` is safe to repeat and also cleans up orphaned children. Startup failure stops the services it launched. Closing the terminal leaves them running; either service exiting stops the other.

`status` shows health, URLs, checkout, and logs. It exits with status 1 when stopped or unhealthy. `doctor` checks Node.js, the Go version from `go.mod`, frontend dependencies, writable state, process inspection, and ports or running service health. Missing Git/GitHub authentication produces warnings; failed startup prerequisites produce exit status 1. Commands accept no additional arguments; use `--help` for usage.

State and combined logs live in `$WHIRLWIND_DATA_DIR/launcher` (default `~/.local/share/whirlwind/launcher`); `whirlwind.log` appends across restarts. `status` and `stop` use the saved instance addresses, so port overrides need only be supplied when starting. Select the same `WHIRLWIND_DATA_DIR` for all commands targeting an isolated instance. Only launcher-owned processes are stopped; stop an existing `make dev` session with Ctrl-C before switching to the CLI on the same ports.

`WHIRLWIND_LISTEN` and `WHIRLWIND_WEB_PORT` override backend and UI ports. The CLI wires the UI proxy to its backend automatically. `WHIRLWIND_SETTINGS_FILE` overrides settings; `WHIRLWIND_ROOT` selects a different checkout. Relative data/settings paths resolve against the checkout. macOS and Linux supported.

See [backend setup and project policy](docs/go-backend.md) and [HTTP API](API.md) for configuration, agent registration, webhook ingress, and execution requirements.

## Engineering loops

- Generate a configurable evidence-backed shortlist, including zero ideas. Select an idea before planning. External ready-labeled issues enter the same candidate queue. Dismiss, defer, or reopen candidates; prior task history stays available.
- Set automatic daily ideation time/timezone or disable it in Settings. Default: 10:00 America/Denver. Manual Run Ideation remains available.
- Set advertised thinking levels per provider/model on Agents. Explicit settings use ACP; provider defaults retain native execution. Inspect actual attempts, models, thinking, and outcomes under Run.
- Answer planner questions before implementation begins.
- Build units in isolated worktrees, respecting dependencies and committed `.whirlwind.yml` or `.whirlwind.yaml` policy.
- Run required checks and review evidence before publication. Review requires a score of at least 9/10 and no unresolved blocking findings.
- Publish a pull request after acceptance and required approval. The user merges it.

SQLite preserves tasks, fenced leases, questions, mutation receipts, ordered events, and commit-keyed context snapshots. Provider routing and recovery keep productive work moving; repeated failures pause for operator input.

## Configuration and naming

- CLI executable: `bin/whirlwind`; backend-only executable: `go run ./cmd/whirlwind`.
- State directory: `~/.local/share/whirlwind`, overridden by `WHIRLWIND_DATA_DIR` or `--data`.
- Database: `whirlwind.db` within the state directory.
- Project policy: `.whirlwind.yml` (execution policy generated and committed during onboarding with required Qlty configuration); existing `.whirlwind.yaml` also supported. Existing app-generated empty starters upgrade through Finish execution setup; custom policies remain operator-managed.
- GitHub labels: `whirlwind:ready` and `whirlwind:work`.
- Optional webhook secret: `WHIRLWIND_WEBHOOK_SECRET`.

Brand renaming changes configuration and persisted identifiers. For an existing installation, stop the service, back up state, move it to the new state location, and rename the database to `whirlwind.db`. Rename and commit project policies, update environment variables and GitHub labels, and review stored task branches, issue markers, and policy hashes before resuming. Existing state is not automatically migrated. A fresh state directory is the simplest starting point.

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
node --test scripts/whirlwind-cli.test.mjs
go test -race ./...
go vet ./...
cd web
npm run build
npm run test:e2e
```

Browser tests start real Go and Next.js servers and use local fixture agents and GitHub commands. No live pull requests are created by the suite. See the [restaurant end-to-end report](docs/restaurant-erp-e2e.md) for historical scenario coverage, simulated boundaries, and unresolved findings.

## Repository

- `cmd/whirlwind/`: local server entry point.
- `internal/`: scheduling, providers, storage, GitHub integration, policy, context, verification, and HTTP API.
- `web/`: Next.js control center and browser tests.
- `bridges/claude/`: optional Claude SDK bridge.
- `docs/`: backend setup and OpenAPI contract.

Whirlwind runs trusted local agent processes. Configure project policy and review agent permission requests for repositories you authorize it to edit. Keep the control listener on loopback; expose only the dedicated, signed webhook listener through your own HTTPS ingress.


### Settings configuration

Settings also lists live provider connectivity and the latest check time. Inbox's **working providers** count uses these same checks; follow its link to `/settings#providers` to identify each working provider and read failure reasons. Pending or failed status refreshes show an unknown count rather than installed-command availability. Checks refresh every 30 seconds while Inbox or Settings is open; set `workers.provider_status_check_seconds` (1–86400) in `settings.yaml` to change the interval. Checks share cached results across tabs and use native protocol metadata without sending inference prompts or installing adapters. Codex checks account/quota metadata, ACP checks session creation, and Claude checks the bridge's model catalog. These checks establish protocol readiness, not successful model inference. While `workers.stopped: true`, connection checks pause and launch no provider processes.

The red **Stop all runners** button sets `workers.stopped: true` in the configured YAML file, cancels current runners, and pauses unfinished tasks. New task, ideation, and provider runners stay blocked across restarts. While stopped, the button becomes **Resume all runners**. It saves `workers.stopped: false` and restores tasks paused by global Stop to their saved stages. Individually paused tasks stay paused. The button is red only while workers are enabled.

`/settings` shows the exact YAML bytes captured when this process started, including comments and formatting, together with its absolute source path and load time. This startup snapshot never changes during the process lifetime. **Edit file** opens the current contents at that same path; **Save file** validates and atomically saves the YAML, rejects stale revisions, and preserves edits on errors. Valid saves apply without restarting; return to the startup snapshot after saving. Legacy `/agents` and `/providers` links redirect to Settings. Root `settings.yaml` remains the default authoritative configuration. On first startup, existing saved worker settings, provider definitions and agent choices seed the file. SQLite retains runtime quota/availability, task/run history, versions and events.

Before routing a Codex provider with saved `quota: exhausted`, Whirlwind rechecks account quota through the native `account/rateLimits/read` RPC, including when model execution uses ACP for explicit thinking. This metadata-only check starts no inference turn and has a five-second timeout. Fresh availability clears stale exhaustion and reset information; failed or unknown reads retain the previous state. Refresh results are recorded in `quota_observations` with `source: routing_refresh`. Missing reset times therefore cannot permanently block a provider after quota becomes available again.

The YAML has `version: 1`, `workers` (capacity, reconciliation and ideation schedule), `providers` (IDs, names, kinds, commands, capabilities and optional discovery commands/cost), and `agents` (role-to-ordered-choice arrays, with optional exact `thinking.config_id` and `thinking.value`). Omit an agent role to keep existing default routing. Invalid files show an error and stop new worker dispatch; running attempts retain their settings. `--settings` / `WHIRLWIND_SETTINGS_FILE` override the file location for isolated deployments or tests; repository development defaults to root `settings.yaml`.

## Bounded local execution

Configure task/discovery budgets and shared quota pools before unattended inference. Selection precedes planning; agents and checks use the shared local OS sandbox. Inbox carries durable milestones, blockers and usage uncertainty. See [local orchestration setup and guarantees](docs/local-orchestration.md) for configuration, recovery, reviewer checks and evaluation.

## Workflow modes

[Personal and Agentic workflows](docs/workflow-modes.md) share coding, verification, review, and a configurable Submitter. Set global `mode` in `settings.yaml` on disk, then restart; a mode change cancels incomplete tasks while preserving their work. Settings exposes integration inventory and restart status.
