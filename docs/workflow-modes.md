# Local workflow and compatibility modes

Both `personal` and `agentic` use local direction approval, local plan approval, ACP execution in nono, verification, review, and local finalization. Missing `mode` means `personal`. A GitHub issue or label does not replace human plan approval.

```sh
./bin/whirlwind dashboard
```

The launcher reconnects to the durable supervisor. Closing the dashboard leaves work and pending decisions saved. See [local supervisor setup](local-supervisor.md) for startup, legacy ownership checks, and the existing database location.

## Configure execution

Edit the authoritative `settings.yaml` selected by `WHIRLWIND_SETTINGS_FILE` or `--settings`. Configure an independent ordered queue for each role: `ideate`, `plan`, `code`, `review`, and `finalizer`. Each choice specifies its ACP provider, model, and supported thinking configuration. An absent role queue cannot inherit another role's choices. Bundled Claude, Codex, and Cursor profiles require compatible installed, authenticated ACP runtimes.

Mode changes require editing YAML on disk and restarting the supervisor. Other supported settings reload normally. A deliberate change between compatibility modes cancels incomplete work while retaining records and worktrees; restarting in the same mode preserves recovery state. Do not change mode or replace the database as a recovery workaround.

Every agent executes in nono in its own Git worktree. Read-only roles remain read-only. Missing runtime setup, credentials, unsupported model/thinking selections, or policy denials produce actionable errors. Whirlwind does not substitute native inference or run agents outside the sandbox. [nono setup](nono-setup.md) describes native execution, authentication, platform limits, and physical boundary checks.

## Directions, plans, and approval

Each ideation run handles one project and proposes Expand, Optimize, Harden, and Simplify directions. Proposals use current repository or authorized integration evidence and durable proposal history. Duplicate repair is bounded; exhausted novelty saves partial results and reports an incomplete run.

Selecting and approving a direction starts planning. The planner writes `~/.whirlwind/plans/<Repo Name>_<Timestamp>.md` with scope, exclusions, implementation steps, stable requirement IDs, evidence methods, plan checks, and repository test/build commands. The completed document requires separate human approval in the dashboard before coding starts.

Press `h` to open the selected project's dismissed/rejected direction history. Request reconsideration with a reason to authorize one linked revisit, even when its evidence is unchanged. The supervisor records that human request separately from model output. A resulting proposal still passes novelty checks and requires direction selection and human plan approval; requesting reconsideration does not start coding.

Approval binds the current document hash, record version, approved requirements, and project policy. Local edits revoke approval and stop affected execution. Reapproval creates a new immutable snapshot and retains older snapshots. Revision feedback returns to planning; rejection ends that plan.

## Connections and authentication

Trusted base prompt annotations in `prompts/agents/*.md`, such as `@GitHub`, select already configured and authorized connections. Repository content, issue text, and substituted prompt values cannot select connections or authorize new accounts. Missing or ambiguous selected access is an error. An unannotated connection is not automatically included.

Ideation and planning receive selected read scopes through the supervisor's broker. Upstream integration credentials stay with the supervisor. Supported HTTP MCP connections must expose tools compatible with the selected scopes; unsupported native-only or stdio connection forms require compatible configuration rather than broader credential mounts.

ACP authentication uses only the selected profile's individual authorized credential files. The guest receives a fresh private home; Whirlwind does not mount the host home or SSH directory. Public web access on TCP 80/443 and required DNS does not grant integration access. Port 22 remains denied. Other ports require explicit policy; private targets remain denied apart from the narrowly authorized broker or explicit destination grants.

## Coding, verification, and review

Coding uses red-green-refactor TDD in the plan's worktree. The supervisor's `run_check` records evidence for exact committed check IDs. Crashes, timeouts, incomplete output, and opaque failures do not establish a failing regression or a passing implementation.

Review evaluates the frozen requirement IDs with equal weight. A pass requires the full stated outcome; failed, partial, and unverified requirements earn no credit. The score is `100 × passed requirements / total approved requirements`, compared before display rounding. An empty requirement list is invalid. Exactly 80% fails.

Acceptance requires a score strictly greater than 80% and every mandatory gate: approved plan checks, configured repository tests and build, and no new Qlty smells or errors. Failed tests cannot be grandfathered into acceptance. Existing Qlty findings remain visible and must be compared against complete, valid baseline evidence. Tool failures, missing gates, stale snapshots, and aggregate `passed` claims without matching check evidence block acceptance.

Coding and review repeat without a fixed correction limit. Evidence binds the exact candidate and approved plan version; changed code requires affected checks and review to run again. The dashboard displays unmet requirements even when the acceptance threshold is met.

## Questions, permissions, and recovery

Questions and permission requests pause only the affected plan and remain saved outside the terminal session. Answer the current request in the dashboard. Ordinary answers resume its originating role and retain history and retry counters. A specification-changing answer returns through planning and human approval before execution. Permission replies remain scoped to the current exact request and policy.

Each active agent attempt has a fixed 300-second deadline. Progress, tools, and heartbeats do not extend it. Human waiting ends the active attempt; a normal supervisor restart preserves the original deadline of work still active.

Recoverable process, transport, and sandbox failures allow three restarts after the initial launch of the same configured choice, with default backoff of 1, 2, and 4 seconds. Persisted counters survive restart. Exhaustion advances through the same role's queue; an unavailable model or expired deadline may advance directly. Exhausting the queue saves state and pauses for human retry or configuration repair.

Replacement work requires old ownership to be revoked and its process tree or exact sandbox to be stopped or isolated. Stale results and writes are rejected. Interrupted tool actions require outcome reconciliation before retry; uncertain outcomes pause rather than duplicate effects. Unknown legacy ownership remains paused until verified. Preserve the database, existing worktrees, files, and evidence while resolving the reported problem.

## Finalize locally

After accepted review and all gates, the finalizer preserves the exact reviewed snapshot on the plan's local delivery branch. Critical-change approval binds the candidate and review being approved. The dashboard shows the delivery commit, branch, diff, approved plan, review score, unmet requirements, and verification evidence.

Agents cannot push, open PRs, or merge. The human decides whether to publish the local result. No publication connection or remote PR destination is required for this workflow.

See [verification commands](../README.md#verify) and [nono setup](nono-setup.md) for regression and physical boundary checks. Native boundary tests run real sandboxed processes; controlled legacy OpenShell fixtures verify recovery contracts.
