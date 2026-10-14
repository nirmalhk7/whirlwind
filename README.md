# whirlwind_

**ENGINEERING IN MOTION**

## Ideas in. Verified software out.

Autonomous software engineering, in continuous motion.

Whirlwind is an open-source, agent-agnostic engineering harness that turns goals into verified software through autonomous execution loops. Bring your agents. Whirlwind handles the engineering process.

Codex, Claude, Cursor, and other ACP agents do the work. Whirlwind coordinates planning, implementation, testing, review, and delivery; preserves durable state; verifies results; and decides what happens next. Human questions, permissions, and critical publication decisions stay visible in Inbox.

**Agent-agnostic · ACP-native · Durable execution · Evidence-driven**

Whirlwind's identity rests on velocity, control, and correctness: maximize verified engineering output per unit of time, cost, and compute.

## Run locally

Requirements: Go 1.25+, Node.js/npm, Git, and authenticated GitHub CLI. Install and authenticate your preferred agents separately.

```sh
go mod download
npm ci --prefix web
make run
```

In a separate terminal:

```sh
make web
```

Open `http://127.0.0.1:3000`. Go serves the local control API on `127.0.0.1:8090`; Next.js forwards same-origin requests to it. Override with `WHIRLWIND_BACKEND_URL` when needed. Install the optional Claude SDK bridge with `npm ci --prefix bridges/claude`.

See [backend setup and project policy](docs/go-backend.md) and [HTTP API](API.md) for configuration, agent registration, webhook ingress, and execution requirements.

## Engineering loops

- Generate directions from repository context, then select a goal for planning.
- Answer planner questions before implementation begins.
- Build units in isolated worktrees, respecting dependencies and committed `.whirlwind.yaml` policy.
- Run required checks and review evidence before publication. Review requires a score of at least 9/10 and no unresolved blocking findings.
- Publish a pull request after acceptance and required approval. The user merges it.

SQLite preserves tasks, fenced leases, questions, mutation receipts, ordered events, and commit-keyed context snapshots. Provider routing and recovery keep productive work moving; repeated failures pause for operator input.

## Configuration and naming

- Executable: `go run ./cmd/whirlwind`.
- State directory: `~/.local/share/whirlwind`, overridden by `WHIRLWIND_DATA_DIR` or `--data`.
- Database: `whirlwind.db` within the state directory.
- Project policy: committed `.whirlwind.yaml`.
- GitHub labels: `whirlwind:plan` and `whirlwind:work`.
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
go test -race ./...
go vet ./...
cd web
npm run build
npm run test:e2e
```

Browser tests start real Go and Next.js servers and use local fixture agents and GitHub commands. No live pull requests are created by the suite.

## Repository

- `cmd/whirlwind/`: local server entry point.
- `internal/`: scheduling, providers, storage, GitHub integration, policy, context, verification, and HTTP API.
- `web/`: Next.js control center and browser tests.
- `bridges/claude/`: optional Claude SDK bridge.
- `docs/`: backend setup and OpenAPI contract.

Whirlwind runs trusted local agent processes. Configure project policy and review agent permission requests for repositories you authorize it to edit. Keep the control listener on loopback; expose only the dedicated, signed webhook listener through your own HTTPS ingress.
