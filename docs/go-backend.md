# Go backend

Whirlwind runs locally on macOS or Linux. Go owns scheduling, worktrees, verification, provider routing, and GitHub writes. SQLite stores versioned JSON aggregates and immutable commit-keyed context index snapshots plus indexed issue identities, fenced leases, webhook receipts, mutation receipts, and ordered events. Existing Python files remain deleted; this is a fresh database without legacy migration.

## Run

Requirements: Go 1.26.9+, Git, authenticated GitHub CLI, Node.js for web UI and optional Claude SDK bridge. Install an agent separately and authenticate using its own documented setup.

```sh
go mod download
go run ./cmd/whirlwind --data /absolute/path/to/whirlwind-state
```

Control server binds `127.0.0.1:9877`. From repository root, run both with `make dev`, or run backend and web UI in separate terminals:

```sh
make run
```

```sh
make web
```

Open `http://127.0.0.1:9876`. The browser uses same-origin `/api/v1` requests; Next.js forwards requests and live events to the Go server. `make web` defaults to `http://127.0.0.1:9877`; override with `make web WHIRLWIND_BACKEND_URL=http://127.0.0.1:PORT` when running Go on another loopback port.

Install web dependencies before first launch, or run web directly:

```sh
cd web
npm ci
WHIRLWIND_BACKEND_URL=http://127.0.0.1:9877 npm run dev
```

For direct `npm run dev` or `npm start`, optional `web/.env.local` can supply `WHIRLWIND_BACKEND_URL`; copy `web/.env.example` to start. Next.js keeps this setting on the server. Restart Next.js after changing it.

For Claude, install the pinned bridge dependency first:

```sh
cd bridges/claude
npm ci
```

Start Whirlwind from repository root or pass absolute `--claude-bridge` path. Default providers are Codex (`codex app-server`), Cursor (`agent acp`), Claude SDK, and an unavailable Antigravity entry. Register other ACP agents through Providers. Use existing provider login with explicit sandbox authentication exceptions. Supported credential files are copied only into isolated runtime state; Whirlwind does not rotate accounts. Availability means executable/dependency exists, not successful login. Quota stays unknown unless provider reports it or rejects a run for quota exhaustion.

## Agents and model fallbacks

Settings combines worker settings, provider commands/login, and agent roles. Its Providers section configures model sources; its Agents section configures Ideation, Planner, Coding, and Reviewer roles. Choose a primary provider/model, use **Load models** to inspect the source's catalog, and add/reorder explicit fallback pairs. **Test model** sends Hello World to the selected model; **Save agent** persists the order across restarts. Model discovery uses ACP session metadata for Codex, Claude, and custom ACP providers; it sends no inference prompt. Native providers launch pinned official ACP adapters via npm, or their optional `model_command` override. Catalog entries are provider-reported IDs, not proof of account access; connection tests verify actual inference.

Configured roles follow that exact order rather than cost/outcome ranking. Quota exhaustion advances to the next available source/model while preserving worktree and handoff; exhausted source quotas apply across its models. Quota exhaustion enters durable waiting and metadata rechecks; setup or budget boundaries pause with a concrete action. Codex receives explicit thread model, ACP uses advertised session configuration/model selection, and Claude receives SDK model option. Unsupported ACP model selection fails explicitly. Unconfigured roles retain existing routing. Task inspection remains at `/tasks`; Agents and Providers are sections of `/settings`, controlled by root `settings.yaml`. Old UI links redirect to those sections.

## Project policy

Register a trusted existing local repository with GitHub `origin`. Project name defaults to the selected directory name and can be edited. Registration checks installed/authenticated `gh`, repository access, and default branch before writing project files. Missing `gh` or failed authentication blocks setup. Install `qlty` and `codegraph` on the backend's PATH. Missing `.qlty/qlty.toml` is configured with `qlty init --yes --no-upgrade-check`; every registration validates it with `qlty config validate --no-upgrade-check`. Missing `.codegraph/codegraph.db` uses `codegraph init`. Initialization must produce nonempty regular configuration/index files. Invalid existing Qlty configuration blocks registration with a repair message; it is not overwritten.

When no policy exists, registration generates a committed execution policy with Qlty and declared checks from committed `package.json`/`go.mod`, scoped source/test/docs directories and tracked non-hidden root files. Required Qlty configuration is committed alongside it so Git worktrees receive it. The generated policy applies high-risk reviews by default and requires publication approval for dependency/database changes. Existing custom policies, unrelated staging and application edits remain unchanged. Edits to existing tracked/staged Qlty config must be committed before generated setup; uncommitted manifests are preserved. Exact unchanged app-generated empty starters upgrade during onboarding or via **Finish execution setup**. Customized empty drafts still permit ideation only and require explicit policy configuration. Setup failures block readiness; unchanged generated files/index entries are restored after a failed commit. Git identity must be configured and active merge/rebase/cherry-pick/revert must finish first. **Confirm committed policy** refreshes the approved hash after later policy edits. Keep one policy filename; `.whirlwind.yaml` remains supported.

Example for a Go project:

```yaml
checks:
  - name: formatting
    command: [sh, -c, 'test -z "$(gofmt -l cmd internal)"']
  - name: build
    command: [go, build, ./...]
  - name: tests
    command: [go, test, ./...]
  - name: static-analysis
    command: [go, vet, ./...]
allowed_paths: ['cmd/**', 'internal/**', 'go.mod', 'go.sum', 'docs/**']
commands:
  - [go, test]
  - [go, build]
  - [go, vet]
risk_rules:
  - level: high
    paths: ['internal/auth/**', 'internal/store/**']
    checks:
      - name: race
        command: [go, test, -race, ./...]
  - level: critical
    paths: ['internal/payments/**', 'internal/permissions/**']
providers: [codex, claude, cursor]
project_slots: 2
```

Commands are argument arrays, not implicit shell snippets. A shell check must explicitly invoke its interpreter. Checks default to ten-minute timeout; override `timeout_seconds`. Optional project `run_timeout_seconds` limits each agent attempt; zero (default) leaves productive runs uncapped. Timeout pauses task for operator decision. Required baseline commands are defined by project policy, remain enabled at all risk levels, and run before AI review. Diff/scope checks are added by server. Risk rules can match module/file `paths`, extension `code_types` (such as `.sql`), or changed exported `api_symbols` (such as `Authorize*`). Selectors within one rule are alternatives; highest matching risk wins. Baseline checks remain required. High/critical changes receive dedicated security and architecture reviews; critical changes require user approval before PR publication.

Allowed paths use glob patterns and directory suffix `/**`; `**` allows repository-wide changes except `.git`, `.whirlwind.yml` and `.whirlwind.yaml`. Agent commands use configured exact argument prefixes when protocol exposes a safely parseable command. Complex shell requests and incomplete permission metadata require Inbox decision. Local agents may implement their own approval behavior; this is trusted local execution, not container isolation. Worktree scope is checked before acceptance.

## Lifecycle

Scheduled discovery follows the configured local time/timezone. It fingerprints repository, GitHub and stored diagnostic inputs before inference, skips unchanged scheduled inputs, and returns zero to the configured maximum evidence-backed candidates. It retrieves bounded relevant history and only requests semantic comparison when deterministic novelty remains ambiguous. Dismissed unchanged evidence stays suppressed. Manual exploration accepts goal, constraints, priorities, maximum candidate count and an explicit fresh flag. Selection records authorization before planner execution; newly discovered ready-labeled GitHub issues cannot bypass it. Ready plans retain the existing trusted publication flow.

Eligible issue label authorizes execution. Webhooks and five-minute reconciliation share repository/issue identity. Issue edits cancel current run and invalidate plan/worktree/approval evidence; old artifacts remain retained. Closure or label removal pauses work. Resume preserves paused stage, including pending planner questions. Explicit Resume authorizes continuing withdrawn issue snapshot; later content changes invalidate that authorization. Issue changes after PR publication pause for review in GitHub; published tasks cannot restart coding.

Units run in independent worktrees; dependencies complete before dependents start. Integration runs serially. Required checks must pass before review. Review accepts score ≥9 with zero unresolved blockers; omitted previous blocking findings remain unresolved. Productive review has no pass cap. Five identical failed attempts trigger new provider/context; three unsuccessful recoveries pause for user decision.

PRs receive `whirlwind:work`. Server pushes branch and creates PR only after acceptance/approval. User merges. State tracks PR closure/merge; worktrees remain retained for inspection and manual cleanup.

Default capacity is four agent processes globally, two per project, configurable in Settings and project policy. Leases renew every 15 seconds for a 60-second term. Single-server lock plus child process registry prevents restarted server reusing worktrees with stale registered processes. Database and process registry belong in private local filesystem, never network-mounted storage.

## Webhook ingress

Expose only webhook listener through user-managed HTTPS ingress:

```sh
WHIRLWIND_WEBHOOK_SECRET=your-secret go run ./cmd/whirlwind \
  --data /absolute/path/to/whirlwind-state \
  --webhook-listen 127.0.0.1:8091
```

Ingress forwards only `/webhooks/github` to port 8091. Never expose control listener. Supply public HTTPS URL during registration; `gh` needs repository webhook administration access. HMAC signature validation is mandatory. Without ingress, reconciliation still discovers eligible issues.

## Optional embeddings

Policy supports `embeddings: {endpoint, model, key_env, enabled}`. Endpoint uses an embeddings-compatible JSON request `{model,input}` and returns `{data:[{index,embedding}]}`. Credential is read from named environment variable, never stored in database. Submit fixture queries, relevant file paths, and acceptance-criterion text to project embedding-evaluation API. Vectors activate only when retrieval recall improves and criterion coverage does not decline. Changed repository index invalidates prior evaluation; rerun fixtures before activation.

## Verification

See [Go adapter and onboarding verification](acp-codex-verification.md) for real Codex new-thread/resume results, ACP regression coverage, and opt-in live test commands.

```sh
go test -race ./...
go vet ./...
cd web
npm run build
npm run test:e2e
```

Go tests use real SQLite and Git worktrees, fake provider processes, and fake external GitHub boundary. Browser suite starts real Go and Next servers. Full onboarding-to-PR scenario uses real storage, ACP client tools, worktrees, verification, and review routing, with local fake external CLIs. Planner/approval interaction also has focused HTTP fixtures. Historical Python-era browser specs remain on disk but are excluded by new test configuration.

## Run evidence and recovery

Schema migrations add bounded artifact storage and attempt event history without
backfilling unknown historical values. Run inspection now exposes exact supplied
inputs, streamed visible output/tools, diagnostics, scoped usage, and linked
verification/review decisions. Startup reconciles interrupted attempts and orphaned
transport requests after process reaping. Publication intent and related progress
writes are durable and version checked. See [durable execution evidence](persistence.md)
for limits, read endpoints, atomicity, and retention behavior.

## Local execution controls

See [local orchestration](local-orchestration.md) for sandbox profiles, explicit authentication/network exceptions, declared check outputs, task/discovery budgets, quota waiting, usage completeness, notification delivery and conservative evidence reuse. New records use existing versioned resource storage; absent historical telemetry stays unknown and legacy tasks require explicit adoption.
