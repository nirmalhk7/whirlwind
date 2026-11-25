# whirlwind_

**ENGINEERING IN MOTION**

## Ideas in. Verified software out.

Autonomous software engineering, in continuous motion.

Whirlwind is an open-source, agent-agnostic engineering harness that turns goals into verified software through autonomous execution loops. Bring your agents. Whirlwind handles the engineering process.

Codex, Claude, Cursor, and other ACP agents do the work. Whirlwind coordinates planning, implementation, testing, review, and delivery; preserves durable state; verifies results; and decides what happens next. Human questions, permissions, approvals, and local delivery evidence stay visible in the terminal dashboard.

**Agent-agnostic · ACP-native · Durable execution · Evidence-driven**

Whirlwind's identity rests on velocity, control, and correctness: maximize verified engineering output per unit of time, cost, and compute.

## Run locally

The dashboard and supervisor use the Go version specified in `go.mod` or newer, Git, and Node.js 20.9+. Native agents and nono-backed checks require macOS and nono 0.81.0. To run an agent, install and authenticate its configured ACP adapter. On Linux, use the dashboard to inspect state or opt in to offline verification for standalone jobs and managed project setup and candidate checks. See the [support and verification matrix](docs/support.md).

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

Projects shows `Git OK` for a valid repository path and `Git ERR` for an invalid path. A failed setup shows `Setup ERR`. Open its Inbox notice to see the reason and retry instructions. Refresh preserves existing project choices and runtime state. Adding, changing, or removing a remote updates metadata for the same checkout. Existing duplicate registrations for a configured checkout appear as one project, with setup and run history from both registrations available in Runs. Agents lists every configured role's ordered model choices, reasoning effort, and provider connection status: green `up`, red `down`, or a truthful `paused`/`unverified` state when no current check is available. Connection checks follow `workers.provider_status_check_seconds` and pause while runners are stopped.

If sandbox recovery blocks new dispatch, the dashboard shows `BLOCK` and the reason instead of `OK`. Press `!` for the full diagnostic. The block clears only after a successful recovery check.

By default, registration queues one automatic ideation run. It waits for successful project setup. Runs identifies the registration trigger and shows why setup is blocking it. Turn off registration and scheduled ideation to register and inspect projects without automatic model calls.

```yaml
workers:
  ideation:
    on_registration: false
    cron: ""
```

Project setup still runs. Operators can explicitly start ideation with `i` in Projects. Coding still requires plan approval.

The Go/tview TUI displays saved plans, questions, permission requests, attempts, review and verification evidence, and local completion data. Inbox occupies the right pane; focus it and press `a` to choose a direction for approval or `h` for direction history. Direction approval and plan approval are separate. Decisions carry expected record versions and idempotency keys over a user-private Unix socket; the supervisor rechecks them before saving. Whirlwind has no browser frontend or UI server.

See [local supervisor and terminal setup](docs/local-supervisor.md), [nono setup](docs/nono-setup.md), [project policy](docs/go-backend.md), and the [HTTP API contract](API.md) for integration and webhook interfaces.

## Engineering loops

- Onboard a local Git repository without replacing its files or existing state. Each ideation run handles one project and proposes Expand, Optimize, Harden, and Simplify directions using current evidence and bounded proposal history. Idea uniqueness comparison is deferred; repeated ideas are allowed and are not labeled as verified unique. A checkout without a remote uses local Git evidence. The legacy hosted workflow checks GitHub issue read access through `gh` before accepting an ideation request.
- Select and approve a direction, then review and separately approve its detailed plan. Plans live in `~/.whirlwind/plans/`. The Planner run for an idea is complete only when its validated Markdown has been saved for review. Approval freezes the requirements and version; later specification changes require another approval. Wait for active approved execution to finish, or explicitly pause it, before saving a changed plan. Saving unchanged content leaves active work running.
- Configure independent ordered ACP agent/model/thinking queues for Ideation, Planner, Coding, Reviewer, and Finalizer in YAML. Bundled Claude, Codex, and Cursor profiles still require compatible installed, authenticated runtimes. Unsupported selections produce errors rather than silent substitutions.
- Every agent runs inside nono in its own Git worktree. Trusted annotations in `prompts/agents/*.md` select already authorized connections; inserted issue or repository text cannot grant access. General public web access does not authorize additional integrations.
- Coding and review repeat until the unrounded requirement score is strictly greater than 80% and every mandatory gate passes. Exactly 80% fails. A successful test process does not count as confirmed regression evidence when a required test is missing or skipped; the recorded check explains why. Gates include the frozen plan checks and configured tests and build. On the managed Qlty path, new findings also block acceptance at every severity and existing debt stays visible. Failed repository tests cannot be grandfathered into acceptance.
- Questions and permissions pause the affected plan. Ordinary answers preserve recovery history; an answer that changes scope returns through planning and human approval. Each active agent attempt has a fixed five-minute deadline; human waiting time is outside that attempt.
- Finalization preserves the exact reviewed snapshot on the plan's local delivery branch. The dashboard shows its commit, diff, review score, unmet requirements, and verification evidence. Agents cannot push, open PRs, or merge; the human decides whether to publish.

SQLite retains execution records, fenced ownership, pending decisions, mutation receipts, ordered events, and proposal history. Recoverable failures use persisted restart budgets and the same role's fallback queue. Unknown legacy runtime ownership remains paused until it can be verified safely.

## Configuration and naming

- CLI executable: `bin/whirlwind`; backend-only executable: `go run ./cmd/whirlwind`.
- State directory: `~/.local/share/whirlwind`, overridden by `WHIRLWIND_DATA_DIR` or `--data`.
- Database: `whirlwind.db` within the state directory.
- Project policy is explicitly approved through `whirlwind policy preview` and `whirlwind policy approve`, then kept in Whirlwind state outside the checkout. Existing committed policies are also supported. See [project policy approval](docs/go-backend.md#project-policy-approval).
- GitHub labels: `whirlwind:ready` and `whirlwind:work`.
- Optional webhook secret: `WHIRLWIND_WEBHOOK_SECRET`.

Whirlwind creates one current SQLite schema directly, without schema versions or data migrations. Reopening a current database preserves its records. Opening an obsolete development layout deletes its database state and recreates the current schema, including `projects`, `ideas`, and `plans`. See [local supervisor setup](docs/local-supervisor.md) and [SQL persistence details](docs/persistence.md).

Target repositories may use any language and supply their own approved commands to the local verifier. Native managed Qlty setup selects only Gitleaks from the catalog and qualifies the installed scanner before recording readiness. The approved Qlty command runs in isolated staging with the approved base exposed as `HEAD`, and scanner state lives outside the checkout. See [project quality setup](docs/go-backend.md#project-quality-setup) for the command and evidence requirements.

After a verification report is saved and runtime cleanup is confirmed, the verifier releases disposable Git staging. It retains the report, journal, configured artifacts, autofix patches, and qualified scanner runtime.

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
node --test scripts/*.test.mjs bridges/claude/*.test.mjs bridges/openshell/*.test.mjs bridges/sandbox/*.test.mjs bridges/nono/*.test.mjs
go test -race ./...
go vet ./...
go build ./cmd/whirlwind ./cmd/whirlwind-dashboard
qlty smells --all
qlty check --all
```

See [development setup](docs/development.md) for the pinned formatters and source checks.

On macOS with the pinned runtime, the native suites can exercise real nono filesystem, network and ACP terminal boundaries. Linux launcher tests cover refusal of the native path. The [offline verifier checks](docs/offline-docker-verifier.md#reproduce-on-the-cloud-runner) exercise the separate Linux backend with a pinned local image. Controlled OpenShell fixtures cover legacy recovery. See [nono setup](docs/nono-setup.md) for native boundary checks.

Settings-driven live provider outcomes and reproduction are recorded in [configured ACP acceptance](docs/acp-configured-acceptance.md). Skipped or incomplete checks do not certify authenticated inference.

See [Releasing Whirlwind](docs/releasing.md) for branch checks, release
dispatch, and artifact verification.

## Repository

- `cmd/whirlwind/`: durable local supervisor and command entry point.
- `cmd/whirlwind-dashboard/` and `internal/dashboard/`: Go/tview terminal interface.
- `internal/control/`: named command/event protocol over a private Unix socket.
- `internal/`: orchestration, ACP providers, persistence, Git, policy, and verification.
- `bridges/nono/`: native sandbox launch and coding scope.
- `bridges/openshell/`: legacy recovery bridge and shared controlled egress/MCP transport.
- `prompts/agents/`: trusted base prompts and connection annotations.

## Settings and recovery

`settings.yaml` remains the authoritative YAML file; `--settings` or `WHIRLWIND_SETTINGS_FILE` selects another path. The configuration contains workers, ACP providers, and independent ordered role queues. Explicitly configure each required role; an absent queue cannot borrow a different role's choices. Invalid settings block new dispatch while running attempts retain their recorded configuration.

A role choice selects its ACP provider, model, and supported thinking configuration. Effective settings and configuration version are recorded for each attempt. Missing installation, authentication, or compatible capabilities produce actionable errors. Metadata-only account/quota checks do not authorize native model inference.

Worker stop/resume settings, pending questions, approvals, deadlines, and restart counters persist outside the terminal session. Closing the dashboard leaves the supervisor running. Normal restart preserves an active attempt's original deadline. Replacements must reconcile old processes, sandboxes, and interrupted tool actions before obtaining write ownership; stale results are rejected.

A recoverable failure allows the initial launch plus three restarts of the same configured choice, with default backoff of 1, 2, and 4 seconds. Exhaustion advances through that role's queue. Exhausting the queue saves state and pauses for human retry or configuration repair. Review corrections have no fixed iteration cap.

[Workflow details](docs/workflow-modes.md) and [local supervisor setup](docs/local-supervisor.md) explain approvals, local delivery, and recovery.
