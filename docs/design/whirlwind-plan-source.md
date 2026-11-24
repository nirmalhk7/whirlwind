# Whirlwind Plan source snapshot

Read from page_3a18b09675088191bbed2c3dcf95deb6, sequence 3035. User decisions in this chat override unresolved or superseded clauses: database migration is waived; breaking schema changes are allowed. One plan per idea; plan complete after local finalization; changed approvals wait for the active version; immutable UTC creation timestamps; four idea types enforced. All idea uniqueness comparison is deferred, including temporary LLM novelty checks; do not add an embedding runtime or sqlite-vec now. ACP profiles must be onboardable through settings.yaml; verify the configured Codex path using the existing authorized login. The November 24 decision limits the default onboarding analyzer set to Gitleaks.

Whirlwind coordinates ideation, planning, coding, review, and local finalization for any onboarded repository. This is the implementation handoff for 6.1 Sol. An approved plan is the unit of execution; do not introduce a separate “task” concept.

# Terminal Interface

- **Interface:** Build a local terminal user interface in **Go** using [rivo/tview](https://github.com/rivo/tview). Use terminal widgets, panels, forms, tables, and text views for the graphical interaction. Throughout this plan, TUI means this terminal application.

- **No UI server:** Do not run a browser frontend, HTTP UI server, or web dashboard. The Go TUI is the human-facing interface. Whirlwind itself is the target Go orchestration service; reconcile existing work-in-progress implementation without assuming code migration is complete.

- **Human interactions:** Provide project and concurrent-plan navigation, direction selection, **editing of saved ideas' What/Why/Where/How in the Whirlwind TUI only while `status = 'draft'`**, separate idea and plan approvals, a **Cancel Planning** action for an idea currently `in_process`, coding clarification forms, and final local-change inspection in the TUI. Preserve the existing approval and permission rules.

- **TUI is presentation and human intent only:** The TUI may display state, launch Vim for plan editing, and submit explicit commands such as approve/reject/cancel to the Go supervisor. It must never choose agents, start ACP processes, create coding/planning jobs, manage leases, or implement approval-triggered transitions itself. The supervisor validates and durably commits the human's decision in SQLite, then returns the stored result for display. Any event emitted after commit is only a wake-up hint; durable database scans, not TUI events, are the authoritative source of dispatch.

- **Live status:** Show agent role and state, effective ACP agent/model/thinking settings, heartbeat health, remaining attempt time, restart count, fallback history, verification results, review score, logs, and diffs. Keep background work from blocking terminal interaction.

- **Supervisor boundary:** Send commands to and receive state from the local supervisor using a local command/event interface; no HTTP UI server is required. The supervisor owns durable execution state, approvals, retries, and sandbox enforcement. The TUI presents state and human decisions rather than independently implementing those policies.

- **Persistence:** Persist pending approvals and clarification questions outside the terminal session. Reopening the TUI must restore them without replaying commands or duplicating approvals. Closing or redrawing the interface must not reset execution deadlines or recovery counters.

# Agent Execution Rules

- **ACP only:** All models used by every Whirlwind agent must be accessed through ACP. Primary and fallback entries come from the human’s YAML configuration, which defines a separate ordered queue of ACP agent, model, and thinking level combinations for each role: Ideation, Planner, Coding, Reviewer, and Finalizer. No direct provider API or alternate transport may bypass ACP.

  - **Bundled ACP configurations:** Ship default ACP configuration profiles for **Claude, Codex, and Cursor**. Each profile defines its ACP launch or connection settings, authentication references, model-selection mapping, and thinking-selection mapping. Include these profiles out of the box; the human’s per-role YAML chooses their order and selected settings. Bundling a profile does not imply its runtime is installed or authenticated.

- **Role configuration:** Load and validate the YAML before starting an agent. Resolve each entry against capabilities exposed by its ACP agent, including the selected model and thinking level. Do not silently substitute an unsupported setting. Keep role queues independent and record the configuration version and effective agent/model/thinking settings for every attempt. Configuration must not override approval gates, role permissions, mandatory verification, or the review threshold.

- **Model and thinking selection:** Each of the three profiles must support explicit model and thinking-level selection through its ACP integration. Discover or validate the choices exposed by the installed integration, map Whirlwind’s configuration to those supported settings, apply them before execution, and record the effective settings. Do not assume identical thinking-level names or behavior across providers. Do not claim success when a requested setting was ignored or is unavailable; surface an actionable compatibility error. Keep model identifiers and supported thinking options configurable rather than freezing a model catalog in code.

- **Configuration verification:** For Claude, Codex, and Cursor, verify configuration loading, model selection, thinking selection, unsupported-setting errors, missing installation or authentication reporting, and clean fallback between configured entries. All three must follow the same ACP-only, nono, role-permission, and durable-handoff rules.

- **nono sandbox required (replaces NVIDIA OpenShell):** Launch every Whirlwind ACP agent and its spawned tools inside a **nono** sandbox using a least-privilege profile for the role. nono uses Linux Landlock and macOS Seatbelt; verify supported capabilities and fail closed on unsupported kernels or policy enforcement failure. Keep Whirlwind's Go orchestrator outside the restricted child process so it can supervise and stop agents. Give Ideation/Planner read-only repository access, and grant Coding/Finalizer only their authorized worktree/output write paths. The planner may write plans only to its assigned output area; database writes go through trusted scoped operations. Apply network and credential restrictions using nono's supported proxy/policy features; test actual egress enforcement for the target OS and version rather than assuming parity with OpenShell. Do not grant broad access to home directories or secrets. A Git worktree alone is not a sandbox. Preserve the same restrictions through ACP fallback. Never silently run unsandboxed; if nono cannot enforce the required policy, report an actionable error and pause.

- **Outbound internet policy with nono:** Support the previously intended general HTTPS/HTTP browsing for authorized agent tasks, but **do not give sandboxed children unrestricted network access by default**. Route traffic through nono's supervised proxy with explicitly tested domain/API-method/path rules and narrowly scoped credential injection; ensure direct socket connections, DNS workarounds and outbound SSH/port 22 cannot bypass the intended policy. Where the host OS/kernel or installed nono version cannot enforce proxy-only egress safely, fail closed and report the incompatibility instead of downgrading to unrestricted access. Outbound access never implies access to unrelated accounts, credentials or filesystem paths.

- **Authentication:** Reuse the human’s existing authorized ACP and integration connections, scoped to the role and selected tools. Do not mount the entire home directory or SSH credential directory to make authentication work. Do not put secrets in prompts, execution traces, or model-selection YAML. Missing required credentials remain an actionable error.

- Multiple approved plans for the same repository may execute concurrently. Each plan has its own delivery branch.

- Every agent has its own Git worktree, including ideation, planning, coding, review, and finalization. Agents must not share a working directory. Read-only roles stay read-only.

- Track the repository snapshot, approved plan version, agent worktrees, and delivery branch for each plan. Use detached snapshots or internal agent branches where necessary; do not check out the same branch in multiple worktrees.

- Transfers between agents must preserve the exact candidate changes. Review evidence and the final local commit must refer to the reviewed snapshot, not another concurrent plan’s changes.

- Direction approval and plan approval are separate human decisions in the TUI.

- Agents have no authority to push, open PRs, or merge. The human ultimately chooses whether to push. PR feedback, PR monitoring, and new conflicts on remote PRs are out of scope.

- The Coding–Reviewer loop continues until the reviewer scores **strictly more than 80%** and all mandatory verification gates pass. **Exactly 80% does not pass.** There is no fixed iteration limit; do not silently impose one.

- Human clarification pauses the affected plan. Agent/model failure follows the recovery rules below rather than being treated as successful completion.

# Roles

## Whirlwind (Go orchestration service)

- **Role:** Whirlwind's main Go orchestration service. It starts and coordinates the specialist ACP agents; it is not itself an LLM agent.

- **Approved-idea listener:** Whirlwind's Go service monitors the SQLite ideas table across **all projects** for rows with `status = 'approved'`, no archive marker, and no linked plan. Resolve archive and plan-link eligibility through durable records linked by `idea_id`; these are not extra columns in `ideas`. For each newly eligible idea, atomically mark it `in_process` **before** spinning up a dedicated Planner Agent instance for that idea. Pass `idea_id`, `project_id`, the source ideation `run_id`, `idea_title`, `idea_type`, and What/Why/Where/How. The Planner Agent retrieves project context and writes a solid plan. Whirlwind itself owns listening, claiming, and spawning; the Planner Agent owns planning. On terminal planning failure, retain `error` (never silently revert to `approved`). A recoverable failed execution attempt is recorded separately while the claimed idea remains `in_process` during the existing restart/fallback policy.

- **Durability and edge cases:** Check the table on startup and periodically, as well as after approval notifications; SQLite alone is not an event stream. Never spawn a duplicate instance for an already claimed idea. Persist worker ownership/leases and recover interrupted `in_process` work after crashes. Limit simultaneous agents with a configurable concurrency cap and queue remaining approved ideas rather than dropping them. Save the `idea_id`–`plan_id` link in durable planning records and set the idea's `status` to `complete` on success; store each attempt failure separately and set the idea to `error` only on terminal planning failure, policy/configuration blockage, or confirmed human cancellation. Do not spawn coding agents until the generated plan receives separate human approval. This is a specification update for the active, partly implemented project, not a claim that the service is already implemented.

## Ideation&#x20;

- **When does it get triggered:** Separately for every project when it is onboarded. Each subsequent ideation run also operates on one project.

- **Permissions access:** Repository read access in the agent’s own worktree; read access to configured GitHub or GitLab repositories, GitHub or Atlassian wikis, and issue platforms such as Jira. Base prompt annotations such as @GitHub select already authorized connections. Missing access must produce an error; annotations do not create authorization.

- **What it does:** Generate up to four distinct, actionable ideas per project ideation run, targeting one novel proposal in each of the four directions:

  - **Expand:** add useful capabilities or address unmet user needs.

  - **Optimize:** improve performance, resource use, or development efficiency.

  - **Harden:** improve correctness, security, and reliability.

  - **Simplify:** remove unnecessary complexity or redesign awkward workflows.

- **Human-readable idea contract:** Every candidate in Expand, Optimize, Harden, or Simplify must include a short **idea title**, its **idea type** (the existing direction/category), and four short, plain-English answers: **What** is the idea? **Why** is it needed? **Where** would it apply (specific repository components, workflows, or files when known)? **How** would it help or make a difference (expected impact, with a concrete example when useful)? It must be understandable quickly without AI filler, lengthy specifications, or invented metrics. The planner—not the ideation agent—expands selected ideas into detailed implementation plans.

- **SQLite `ideas` table (agreed schema):** Store one row per candidate idea with eleven columns: `idea_id`, `project_id`, `run_id`, `idea_title`, `idea_type`, `what`, `why`, `where`, `how`, `status`, `timestamp`. `project_id` is a foreign key to `projects(project_id)`. Keep `run_id` as the shared source ideation-run identifier, separate from each idea's unique `idea_id`. There is no `when` column. A project ideation run targets at most one genuinely novel displayed idea per direction; missing novelty must not be fabricated.

- **SQLite `projects` table (agreed schema):** Keep five columns: `project_id`, `project_name`, `project_path`, `status`, and `timestamp`. Define `project_id` as `TEXT PRIMARY KEY NOT NULL`, with the value `project_` followed by a UUID; generate and validate the UUID suffix in the service. `project_name` is the display name and `project_path` is the repository path. Define the relationship from ideas as `FOREIGN KEY (project_id) REFERENCES projects(project_id)`, using matching ID types. Enable SQLite foreign-key enforcement on every connection with `PRAGMA foreign_keys = ON` and verify that an idea cannot reference a missing project. Define project status as `TEXT NOT NULL CHECK (status IN ('draft', 'approved', 'error', 'complete'))`; all other values must fail with an error.

- **Idea ID in SQLite:** Define `idea_id` as `TEXT PRIMARY KEY NOT NULL`, with the value `idea_` followed by a UUID, for example `idea_550e8400-e29b-41d4-a716-446655440000`. SQLite has no native UUID type, and this prefixed identifier is stored as text. Generate and validate the UUID suffix in the service; retain the same immutable ID for the idea and all references to it. `run_id` identifies the source ideation run, not the idea, and may be shared by multiple ideas.

- **Column meanings:** `project_id` references the owning row in `projects`; `run_id` identifies the source ideation run; `idea_title` is the short display title; `idea_type` replaces the former `direction` field; `what`, `why`, `where`, and `how` hold the four plain-English answers; `status` replaces `decision_status` and keeps the existing five-state idea lifecycle; `timestamp` records the idea's timestamp. `how` replaces `how_it_helps` without changing its meaning. Store `idea_type` as text with the existing categories `expand`, `optimize`, `harden`, and `simplify`. SQLite can restrict these values with a `CHECK` constraint, but that specific database constraint remains a proposal rather than a confirmed decision.

- **Supporting durable records:** The eleven-column ideas table does not contain all operational state. Keep plan links, archive markers, errors/cancellation reasons, approval snapshots, repository revisions, novelty outcomes, duplicate/related idea links, vectors, audit timestamps, leases, fencing generations, and retry/checkpoint data in durable linked records outside `ideas`, keyed by `idea_id` where applicable. Resolve project names through `projects` using `project_id`. Preserve existing data and transactional approval/claim/completion guarantees when reconciling the physical schema; do not add these as extra `ideas` columns or discard them.

- **Run and idea identity:** One project ideation run can produce up to four accepted ideas. Each has its own `idea_id`; all ideas from that run share `run_id` and `project_id`. Retain run-level context for per-direction novelty limits, partial-success reporting, and crash recovery without conflating the run with any one idea. Separate Planner/Coding/Reviewer/Finalizer execution and attempt identities remain necessary for retries and fencing.

- **Idea lifecycle (five statuses):** Define `status` as `TEXT NOT NULL CHECK (status IN ('draft', 'approved', 'in_process', 'error', 'complete'))`. Keep `in_process` for active planning; every other value must fail with an error. Use the exact value `complete`, replacing the former idea-status spelling `completed` without losing history. Newly accepted ideas start as `draft`. **Only draft ideas may be edited**; ideas in `approved`, `in_process`, `error`, or `complete` are read-only. Enforce this at both TUI and database/service mutation boundaries with a conditional status check to prevent edits racing with approval; revalidate edited draft content against the same-project/same-direction novelty gate, excluding its own current record, before approval. Atomically associate the accepted content version with its embedding/index; a stale or failed novelty result must block approval, not approve changed content. Human approval for planning sets `approved`; Whirlwind's supervisor claims an approved unplanned idea and sets `in_process` before launching its Planner Agent. If planning reaches terminal failure or the human cancels planning, set `error` with a specific reason (`user_cancelled` for human cancellation) and allow deliberate retry only by explicit user action; if the plan is successfully created, record the plan link in durable planning records and set `complete` (meaning planning of this idea completed, not coding). Monitor/claim idempotently to prevent duplicate plans. Keep deleted/closed ideas archived rather than using a sixth status; retain their vectors for novelty checks, but exclude archived records from planner intake. Keep novelty outcomes separate. Idea approval never authorizes code execution: the finished plan requires separate human approval.

- **Pre-insertion novelty gate (v1):** The ideation agent owns generation, embedding, vector retrieval, threshold decision, and retries. Embed canonical What/Why/Where/How plus mechanism; query `sqlite-vec` only within the **same project AND the same direction** (`expand` against `expand`, `optimize` against `optimize`, `harden` against `harden`, `simplify` against `simplify`), matching the candidate's `project_id` and `idea_type`, regardless of `status`. Never compare different projects or different directions. Use the maximum cosine similarity to prior ideas and accepted candidates in the current batch: **strictly below 0.60** qualifies as novel, **0.60 or above** is similar and must be regenerated. Do not invoke an LLM novelty judge in v1. **If embedding generation or vector search fails, fail the affected ideation run with an explicit infrastructure error; do not bypass the novelty gate or pretend the candidate is unique. Preserve already saved successful directions.** Allow at most **five generated candidate ideas per project per direction per ideation run**, configured with `ideation.max_attempts_per_direction: 5` in `settings.yaml`. Count the initial candidate as attempt one. If all five have maximum cosine similarity >=0.60, mark that direction's ideation attempt as **error**, record the failure, and notify the human; never produce a fabricated unique idea, and persist qualifying ideas with the eleven agreed core fields and their linked novelty/vector records before displaying them. Retain declined ideas in the index for future comparisons; duplicate attempts may be logged separately for audit, but are not shown as new ideas. Use transactional safeguards for concurrent insertions. The 0.60 cutoff is configurable and must later be calibrated to the embedding model.

- **When does it pass:** All successfully generated directions are available as draft ideas in the TUI, with per-direction errors visible for unsuccessful directions. The human approves an individual draft idea, not merely its direction; Whirlwind then dispatches that approved idea to a Planner Agent.

- **Human notified:** Directions ready for selection, or an error.

## Planner

- **SQLite `plans` table (agreed schema):** Keep five columns, in this order: `plan_id`, `idea_id`, `plan_path`, `status`, `timestamp`. Define `plan_id` as `TEXT PRIMARY KEY NOT NULL`, with the value `plan_` followed by a UUID; generate and validate the UUID suffix in the service. Define `idea_id` as a foreign key to the ideas table: `FOREIGN KEY (idea_id) REFERENCES ideas(idea_id)`, using matching ID types. `plan_path` is the path to the real Markdown plan document. Enforce foreign keys on every SQLite connection and reject references to missing ideas.

- **Plan status constraint:** Define `status` as `TEXT NOT NULL CHECK (status IN ('draft', 'approved', 'error', 'complete'))`. The same four choices apply to `projects`; `ideas` additionally permits `in_process`. Reject invalid or null status values on insertion and update; do not silently accept or coerce them. The idea-type `CHECK` remains proposed and is not approved by this status decision. Creating and verifying the Markdown document produces a `draft` plan, not human approval. Preserve explicit approval, frozen versions, and separate durable execution state; the exact transition to the plan's `complete` state remains to be specified.

- **SQLite `planner_runs` table:** Maintain one persistent table with **one row per Planner Agent execution/attempt**, rather than a new table per execution. Required columns: `planner_run_id` (unique primary key), `idea_id` (foreign key to ideas), `idea_run_id` (source ideation run ID, matching the referenced idea's `run_id`), `timestamp` (creation/start time), `status` (execution status), and `markdown_path` (path to the generated plan Markdown file; nullable while running or on failed attempts). **Existing execution-status semantics:** `in_process`, `completed`, `error`; these are distinct from the newly constrained statuses of `ideas`, `projects`, and `plans`. Whether this older operational table is retained or reconciled with the new plan schema remains an open decision; do not silently replace its execution states with the four domain-table values. Record the Planner execution before spawning. **Every successful plan must produce a real Markdown file**, using the configured plans directory and a collision-safe filename. Whirlwind must verify the file exists and is readable, then persist the verified path in `plans.plan_path` and the existing `planner_runs.markdown_path` link alongside `plan_id`. Atomically mark the planner execution `completed`, the idea `complete`, and keep the newly generated plan `draft` pending human approval. A missing, empty, unwritable, or unverified output file is a planning failure: set `error` with details rather than falsely reporting completion. If SQLite recording fails after file creation, reconcile the existing file and run on recovery rather than producing duplicate plans. Retain each attempt for audit, including retries with new planner run IDs. Track completion time and error reason as additional operational fields. Coordinate the linked records so no launch is lost or doubled after restart.

- **Planner intake / durable monitoring:** Whirlwind's Go supervisor owns an approved-idea watcher that dispatches a Planner Agent for **every eligible approved idea across all projects and ideation runs**, not just the latest idea or current run. The TUI only submits the human approval decision; it never launches an agent. The supervisor's durable polling/queue-dispatch loop checks SQLite for ideas with `status = 'approved'`, no archive marker, and no linked plan, using the supporting durable records at startup, periodically, and after approval events; never rely solely on ephemeral in-memory event notifications. Atomically claim each eligible idea and transition it to `in_process` before starting work, so concurrent planners cannot double-process it. Use persisted claim/lease ownership and recovery of stale `in_process` work after crashes, without producing duplicate plans. Upon successful plan creation, atomically record the `idea_id`–`plan_id` link in durable planning records and set the idea's `status` to `complete`; on terminal failure record reason and `error`, leaving deliberate retry semantics. Recoverable attempt failures follow the bounded restart/fallback policy while the idea remains claimed `in_process`. Process all qualifying ideas fairly, with bounded planner concurrency; no approved idea may silently disappear or be skipped due to restart, backlog, or a transient poll failure. **Human cancellation:** The TUI must let the human cancel a specific active Planner Agent. Whirlwind durably records the cancellation request, prevents that worker from committing late results, stops/fences its ACP execution and sandbox, and records the planner-run outcome with a cancellation reason. It must not affect other planner runs or silently requeue the cancelled idea. Upon confirmed cancellation, set the idea `status = 'error'` and the planner run `status = 'error'`, recording `error_reason = 'user_cancelled'` (or equivalent reason metadata). Never automatically requeue a user-cancelled idea; any subsequent retry requires a fresh explicit user action. Do not invent a sixth status. A blocked implementation may separately require a revised specification. Idea approval permits planning only; coding still requires separate human plan approval.

- **Permissions access:** Repository read access in its own worktree, the same prompt-selected integrations as ideation, and write access to `~/.whirlwind/plans/`. Use a capable thinking model.

- **What it does:** Write a detailed specification aligned with the selected direction to a collision-safe path under the configured plans directory (default `~/.whirlwind/plans/`), for example `<sanitized-repo-name>_<timestamp>_<plan_id>.md`. Include scope, exclusions, implementation steps, plan checks, and configured repository test and build commands. Give every requirement a stable ID, concrete outcome, and evidence method.

- **When does it pass:** Planning completes when a valid Markdown plan is generated, verified on disk, and linked through `plans.idea_id` and the existing `planner_runs` record. Set the idea to `complete` and the planner execution to its existing `completed` state; the generated plan has `plans.status = 'draft'`. **This is separate from human plan approval.** The plan remains awaiting human approval in the TUI; freeze its requirements/version on that later approval and set `plans.status = 'approved'`. Never trigger coding merely because planning finished.

- **Human notified:** Plan ready for approval, errors, or required clarification.

### Fresh Directions with Bounded Context

- **When does it get triggered:** During every ideation run, before proposals are published. Expand, Optimize, Harden, and Simplify remain the four categories; the concrete proposals inside them must be new.

- **Permissions access:** The ideation agent gets bounded access to idea-history retrieval filtered by both the current project ID and the candidate's direction (within that project only), plus read-only current-project repository evidence. It orchestrates embedding checks, retries, and registry writes through scoped supervisor-provided tools, not unrestricted direct SQLite access. Retrieval and generation still respect ACP and nono boundaries.

- **Durable memory:** Use the eleven core idea fields plus durable records linked by `idea_id` to retain each proposal's ID, project ID, idea type, problem, proposed mechanism, affected components, evidence references, repository revision, status, rejection or deferral reason, and links to related proposals and approved plans. Keep complete records outside the prompt. Retain rejected, deferred, implemented, and previously shown proposals so old suggestions do not become “new” merely because they fell out of the context window.

- **Bounded input:** Start each run with current project goals, a compact repository map, changes since the last completed run, and relevant issue or wiki updates. Use a fixed token budget for retrieved history instead of appending past conversations. For v1, use a configurable 4,000-token history budget and at most 12 compact proposal records per retrieval; these are implementation defaults, not growing limits. Bound total retrieval tokens across the run as well; do not keep adding tool results to the same session indefinitely.

- **Targeted retrieval:** For novelty screening, filter SQLite vectors by **`project_id` AND `idea_type`**, across all idea statuses; resolve the project name through project records for readability, while `project_id` ensures stable matching after renames. Use `sqlite-vec` nearest-neighbor retrieval and maximum cosine similarity against historical and same-batch candidates. Optional structured/keyword retrieval may add context but must not override the v1 <0.60 rule. Keep retrieval bounded; declined and approved ideas remain searchable.

- **Generate from evidence:** Prioritize changed or previously unexplored components and unresolved needs. Track which areas and categories were explored and rotate attention between runs. Every candidate must identify a current problem, supporting repository or integration evidence, a concrete change, and its expected benefit. Changing a title or choosing a random area is not sufficient novelty.

- **Check before publication (v1):** First reject exact normalized fingerprints within the same project and direction. Then use the `sqlite-vec` cosine similarity threshold from the v1 novelty gate: **below 0.60** is provisionally unique, **0.60 or above** is rejected. Do **not** invoke a separate ACP/LLM novelty judge in v1 (the proposed three-band LLM approach is future work). Compare with previously accepted ideas in the same run only when project and direction match. Repository checks for externally implemented features are a future enhancement, not a required v1 gate. A different title or wording cannot bypass the configured checks.

- **Repair only the duplicate:** Return the nearest prior proposal IDs and a short reason to the generator. Regenerate only the affected category, preserving accepted candidates. Persist rejected candidate fingerprints and reasons outside the prompt; supply only the relevant compact exclusions on retry. Commit publication and uniqueness checks together so concurrent runs cannot publish the same proposal unnoticed.

- **Revisiting old proposals:** Do not silently resurface an earlier direction as new. A changed repository, user goal, or new evidence may justify reconsideration, but label it as a revisit, link its predecessor, and explain what changed. Rejected proposals remain in the novelty index. Revisit labeling alone never bypasses the v1 novelty gate; any human override/reuse workflow is an unresolved decision, not implicit permission to publish a duplicate.

- **When does it pass:** Each direction that yields a valid candidate within five generations, using the same-project and same-direction v1 novelty gate, is saved and shown as a draft. A failed direction is reported as an error without discarding successful other directions; the overall run is partial when fewer than four succeed. Keep history retrieval bounded; do not invent novelty to fill missing directions.

- **When fresh ideas run out:** Do not fabricate four proposals or endlessly paraphrase duplicates. After the five-candidate novelty limit for a direction is exhausted, **preserve and display successful ideas from the other directions**, and record/report **an error only for the exhausted direction**. A run with three successful ideas and one failed direction retains those three ideas and is explicitly partial, not a successful four-direction result. This limit applies to ideation novelty repair, not to the Coding–Reviewer loop.

- **Verification for 6.1 Sol:** Exercise a repeated proposal under a new title, an old rejected proposal outside the recent-history window, the fact that external-implementation detection is deferred (do not require this future gate in v1), a revisit with changed evidence still subject to the v1 gate, concurrent duplicate publication, and a long project history. Assert that prompt history stays within the configured token budget and exhausted novelty is reported honestly.

## Coders

- **When does it get triggered:** On human approval of a frozen plan, Whirlwind's Go supervisor launches the Coding and Reviewer Agents side by side (never from the TUI or another agent). Coding continues after reviewer correction under the same approved-plan workflow.

- **Approved-plan watcher and coding dispatch:** After the human edits a Planner-generated Markdown plan in Vim and approves it through the TUI, the supervisor validates and persists the exact content, an immutable approved version/hash, project_id, and approval time. Set `plans.status` to `approved` and show **Plan Approved** in the Planner table, while keeping the completed Planner execution status unchanged. The TUI must not launch a Coding Agent. A supervisor-owned persistent watcher scans SQLite on startup, periodically, and after commit notifications for eligible approved plans across every project and planning run whose frozen approved version has never been claimed for execution. A missing active worker is not proof of eligibility: terminal/completed/paused/error workflows must not be redispatched by the approval watcher. For each plan/version, atomically claim it and create its durable coding run/lease in one transaction; only the winning claim may dispatch. Enforce durable uniqueness of the workflow claim for `(plan_id, approved_version)`; recovery and explicit retries resume that workflow with new attempt identities rather than inserting a second approval-triggered workflow. Respect concurrency limits and fair queuing. Resolve the correct project and repository, create/reconcile an isolated delivery branch and Git worktree, select the first compatible ACP agent/model/thinking entry from the Coding role's ordered YAML queue, and launch it inside the role-scoped nono sandbox with the complete frozen approved plan and acceptance criteria. The agent implements the full approved scope. On crash/restart reconcile claims, worker processes, sandbox, worktrees, and effects, fence stale ownership, and safely resume/retry without duplicate coding runs. Reviewer correction is a continuation of the same plan's coding workflow, not a new approval-triggered launch. Failures follow the existing durable error/restart/fallback rules; never silently discard a plan. Later modifications to an approved plan require a new explicit approval/version before new requirements take effect.

- **Permissions access:** Read the frozen approved plan and review findings; write implementation changes only in the Coding Agent's own isolated worktree/branch. The Coding Agent may execute development commands but does not control or waive mandatory post-coding verification, which the Go supervisor triggers through the generic verification runner. No authority to change approved requirements, verification gates, or quality policy.

- **What it does:** Implement the frozen plan, address reviewer findings, and submit immutable candidate snapshots/diffs. Qlty and configured analyzers run through the generic verification runner; scanner source-code autofixes are allowed only in isolated staging and must be incorporated into a new candidate, reverified, and rereviewed. Keep all scanner-owned configuration, caches and reports outside the actual project repository. Introduce no new quality smells or errors relative to the frozen baseline, including findings classified as non-blocking. Pre-existing findings may remain.

- **When does it pass:** Its candidate is ready for reviewer evaluation. Coding completion alone is not final acceptance. Final acceptance requires the reviewer’s score to be **greater than 80%**, every approved plan check, the repository’s configured tests and build, and no new quality smells or errors, including non-blocking ones. Pre-existing findings may remain.

- **When blocked:** Save work and ask a specific question in the TUI. Pause the affected plan until answered. Resume within approved scope; if the answer changes the specification, route it through the planner and human plan approval first.

- **Human notified:** Errors or a coding clarification requiring an answer. Ordinary code corrections continue through the review loop.

## Reviewers

- **When does it get triggered:** Whirlwind starts Reviewer alongside Coding when the plan is approved. Reviewer may prepare the approved-plan acceptance checklist immediately, but actual code evaluation begins only after receiving the exact immutable candidate snapshot and its completed post-coding verification evidence. Review each revised snapshot separately; never inspect the Coding Agent's mutable worktree.

- **Permissions access:** Read-only access to the approved plan, candidate diff, relevant source, and verification evidence in its own worktree containing the exact candidate snapshot. It does not modify implementation code.

- **What it does:** Evaluate the frozen requirement IDs using the rubric below. Return actionable findings, unmet requirements, and the evidence supporting each judgment.

- **When does it pass:** The unrounded score is **strictly greater than 80%**, and every mandatory verification gate passes for the same candidate snapshot and plan version.

- **When it does not pass:** Return findings to the coding agent. Coding corrects the candidate and review runs again. Continue this loop until both the score and gates pass; do not finalize at exactly 80%.

- **Human notified:** Errors. Review findings ordinarily go directly back to coding.

### Plan Adherence Rubric

- Every approved requirement has equal weight. Record **pass**, **fail**, or **unverified** for each ID, with code references, verification evidence, and an explanation of gaps.

- A pass requires the full stated outcome. Partial implementation and unverified requirements earn no credit.

- **Score = 100 × passed requirements / total approved requirements.** Compare the unrounded score with 80; round only for display. An empty requirement list is invalid.

- Neither coder nor reviewer may delete, split, reweight, or weaken approved requirements to improve the score.

- **Mandatory gates:** All required project-onboarding verification commands, applicable configured Qlty/security checks, and approved-plan mandatory checks must pass for the same frozen snapshot and plan/configuration versions. A generic post-coding verification runner executes every command, records all outcomes (without stopping at the first failure), and provides evidence to Reviewer. Whirlwind's Go service only triggers and tracks the runner; it does not hard-code language-specific testing. No new quality smells or errors may be introduced relative to the frozen baseline, including findings classified as non-blocking; pre-existing findings may remain. A failed, missing, or unexecuted required check cannot be overridden by a high Reviewer score.

- Display unmet requirements even when acceptance passes. A score above 80% does not mean 100% plan completion.

- Bind review and check results to the exact candidate snapshot and approved plan version. Subsequent changes invalidate affected evidence; rerun verification and review before finalization.

## Finalizers

- **When does it get triggered:** Only after reviewer acceptance: score **greater than 80%** and all mandatory gates passing.

- **Permissions access:** Its own worktree with the accepted snapshot; permission to create the plan’s local delivery commit and update the TUI. No push, PR creation, or merge authority.

- **What it does:** Commit the accepted changes locally. Display the delivery branch, commit, diff, approved plan, review score, unmet requirements, and verification evidence.

- **When does it pass:** The accepted changes are committed locally and the result is visible in the TUI for human inspection. The human decides whether to push.

- **Human notified:** Errors. Successful local completion is displayed in the TUI.

# Recovery and Model Fallback

- **Process heartbeats:** Supervise every role’s worker, ACP process, and nono sandbox. Use a worker heartbeat independent of model output, with implementation defaults of one heartbeat every 5 seconds and lease expiry after 20 seconds without a heartbeat. Track process exit, sandbox health, last heartbeat, last ACP progress, active tool, execution ID, and attempt deadline separately. A healthy heartbeat proves process liveness, not useful model progress, and never extends the five-minute deadline.

- **Restart budget:** On recoverable process, ACP transport, or sandbox-runtime failure, try bringing up the same configured agent/model/thinking combination **up to three restart attempts after the initial launch**: four launches maximum per queue entry and recovery episode. Use bounded backoff, with default delays of 1, 2, and 4 seconds. Persist counters so supervisor restart cannot reset the budget. Reset recovery counters only after the role completes its stage or a human explicitly starts a new retry episode; heartbeats alone do not reset them.

- **Fallback after restart failure:** If all three restarts fail, select the next available agent/model/thinking entry from the role’s YAML queue. Apply the same bounded recovery to that entry. A known unavailable or unsupported model, or an expired hard deadline, may advance directly to fallback rather than repeating a futile launch. Each new execution attempt has its own strict 300-second deadline.

- **Single active owner:** Before restarting or falling back, revoke the failed execution’s ownership, stop its process tree or isolate its sandbox, and reconcile partial effects. Persist a lease and execution generation; reject heartbeats, results, and writes from stale owners. Do not grant a replacement write access until the old writer is stopped or isolated. Supervisor recovery must reacquire ownership and reconcile state before launching a replacement.

- **Recovery verification:** Inject crashes in every role, lost heartbeats, ACP disconnects, sandbox-runtime failures, and supervisor restarts. Verify three restart attempts, persisted counters, fallback model/thinking selection, no duplicate local commits or tool effects, rejection of stale results, and preserved nono restrictions.

- **Strict attempt timeout:** Every agent execution attempt has a hard wall-clock deadline of **5 minutes (300 seconds)** from its start. This applies to all roles and primary and fallback models. Progress, streamed output, heartbeats, and running tools do not reset or extend it. This is not an inactivity timeout.

- **On deadline:** Mark the attempt timed out, stop or fence its ACP execution and spawned work, preserve recoverable state, reconcile interrupted actions, and advance to the next available entry in the same role’s YAML fallback queue. A replacement attempt gets its own five-minute deadline. Reject late results from the timed-out execution and prevent it from continuing to modify files.

- **Scope of the limit:** Five minutes limits each execution attempt, not the total approved plan or the whole Coding–Reviewer loop. A completed stage, completed handoff, or persisted TUI question ends the active attempt. Waiting for human input is a paused workflow, not an active model attempt. Supervisor restarts preserve an active attempt’s original deadline rather than granting extra time.

- **When does it get triggered:** A supervisor restart, worker or child-process exit, expired heartbeat lease, ACP disconnect, sandbox-runtime failure, model error, interrupted execution, or the strict five-minute deadline for any role.

- **Permissions access:** Persisted execution state and the affected agent’s existing role permissions. Fallback does not expand permissions.

- **What it does — recovery for ALL agent roles:** Persist durable per-run/per-stage checkpoints for Ideation, Planner, Coding, Reviewer, and Finalizer, and route any role failure through supervised restart/fallback. On Whirlwind restart, enumerate interrupted executions, reconcile each run's persisted state, worker process/sandbox, ownership lease, and completed side effects before deciding whether to reconnect or relaunch. Resume from the last safe checkpoint using the same approved requirements, permissions, sandbox policy, ACP role/model fallback queue, and original timeout/restart accounting. A model's hidden in-memory conversation state is not guaranteed to survive; reconstruct context from durable snapshots and records. Record failures and effective configurations; never borrow another role's queue or silently modify thinking settings.

- **Clean handoff:** Preserve the approved direction and plan version, current files and diff, agent worktree identity, completed tool results, verification evidence, pending TUI questions, and review findings. Supply this durable handoff to the replacement ACP session; do not assume sessions or hidden model state transfer between different models. Reconcile interrupted tool actions before retrying so fallback does not duplicate or overwrite completed work. Revalidate changed code before acceptance.

- **Failure boundaries:** Every role execution failure is recorded and handled by the recovery supervisor. Transient runtime failures use the restart budget; model unavailability and hard deadlines use fallback. Failed tests and low review scores stay in the Coding–Reviewer correction loop. Missing credentials, invalid configuration, and explicit permission or sandbox-policy denials pause with an actionable error when no already-authorized compatible entry can proceed. Restart or fallback never bypasses authorization or runs an agent unsandboxed.

- **When does it pass:** The next available model continues the affected stage through ACP inside nono, using saved state, and eventually meets that stage’s pass condition. No stale execution continues writing. Recovery does not bypass approval, review, verification, or sandbox policy.

- **When all fallback models fail:** Save state, pause the affected plan or ideation run, and notify the human. Resume after the human retries or changes the queue.

## Project-agnostic post-coding verification (agreed November 10, 2026)

- **Onboard each project with its own verification command set.** The human defines and approves commands and their execution order, required dependencies/environment, per-command timeouts, and applicable quality/security tools. Whirlwind remains language/framework/build-system agnostic: it does not infer or hard-code `go test`, `npm test`, or any other project commands. Changes to mandatory commands or the quality policy require explicit human approval and a new configuration version; Coding/Reviewer cannot silently weaken or disable gates.

- **Who triggers versus who runs:** Only the Go supervisor controls agent launches, workflow transitions, and the post-coding trigger. When Coding submits a completed candidate through ACP, Whirlwind freezes the candidate identity (Git tree/commit or equivalent) and approved plan/configuration version, then dispatches a **generic, non-LLM verification runner**. The runner executes near the project checkout and dependencies, possibly on a different host than the central Whirlwind service, using an isolated checkout/worktree and a restrictive nono policy. The generic runner—not the Go supervisor process and not the Reviewer Agent—actually executes the commands. Treat remote runner/transport mechanics as an implementation detail to validate; do not assume local path access on the service host.

- **Run *all* configured commands**, even if an earlier one fails. Record results individually: command/config version, immutable snapshot ID, status (pass/fail/error/timeout), exit code, stdout/stderr/log locations, start/end time, and any generated evidence. Do not short-circuit on the first failure. Handle dependencies between commands explicitly and record blocked/unexecutable commands as errors rather than treating them as passes. All mandatory configured checks must actually succeed before final acceptance.

- **Immutable review handoff:** Start Coding and Reviewer **side by side** on plan approval, with unique existing run IDs, independent worktrees and least-privilege access. Reviewer may prepare acceptance criteria immediately, but cannot inspect mutable implementation files or score them until the immutable candidate snapshot and the complete runner report arrive. Any source-code change, including an automated fix, produces a **new** candidate snapshot and requires verification and review again. Finalizer commits only the exact accepted snapshot.

- **Qlty as the central analyzer:** Use Qlty as the default configurable, language-agnostic quality-analysis entry point where supported. Enable **Gitleaks** (secrets) in the default project-onboarding analyzer set where compatible; provide **Trivy** (dependency/config/image vulnerability scanning) as an optional applicable analyzer and **ast-grep** for optional custom structural rules. Verify actual Qlty plugin/integration availability and individual scanner capabilities before relying on one invocation; expose external commands when an integration cannot be validated. Missing/failed required tools are errors, not green results. Findings are bound to the candidate and handed to Reviewer. Compare all newly introduced quality smells and errors to the agreed frozen baseline, including non-blocking findings. Any such new smell or error fails the quality gate; pre-existing findings may remain. Scanner support remains project-specific, and severity classifications cannot exempt a new smell or error.

- **Do NOT pollute source repositories with Qlty's own files.** The human can choose an external per-project directory for Qlty configuration, caches, logs, reports, and generated artifacts, defaulting under `~/.whirlwind/projects/<project-id>/qlty/`. The original repository must not receive a `.qlty/` directory or scanner-owned configuration/caches. Since relocation of Qlty's entire configuration directory is not confirmed as a native CLI feature, Whirlwind's generic runner stages a **temporary, isolated checkout** and makes external Qlty configuration available there, with symlink/copy mechanics only if tested on target OS/plugin combinations. Audit actual file writes (including plugins, caches, and symlink traversal); clean up staging without deleting retained configured evidence.

- **Autofixes are allowed.** Do **not** universally force `--no-fix`; scanner autofixes to source code are acceptable. Run them on isolated staging content, capture exact patch, and integrate only intended source changes through the Coding workflow. Never silently mutate an already submitted frozen candidate or bring tool-generated `.qlty/` files into the real repository. Re-snapshot and rerun every mandatory check after an applied autofix.

- **Qlty local-change scope:** The desired option is to inspect only locally modified/new files without requiring a remote upstream or comparison to `main`. Do **not** assume `qlty check --no-fix` alone correctly limits the file scope: it disables fixes, not necessarily repository-wide traversal. At onboarding and before implementation, verify the installed Qlty CLI's supported diff/staged/working-tree and explicit path modes, including untracked files, then select local paths from `git status --porcelain` if necessary. Treat exact Qlty invocation and untracked-file behavior as **to be validated**, not a settled command.

- **Acceptance:** Enforce the pre-existing strictly >80% Reviewer requirement score **and** 100% pass of configured mandatory verification checks for the same frozen candidate and plan/config versions, with no new quality smells or errors, including non-blocking ones, relative to the frozen baseline. Pre-existing findings may remain. All results must remain available to the TUI and to coding feedback. Whirlwind never implements project-specific lint/test logic itself.

## Explicit supervisor transitions and approval transactions (agreed)

- Keep distinct persisted states for idea lifecycle, plan authoring/completion, plan approval, coding, verification, review, and local finalization. The Go service must reject illegal transitions and accept only valid human approvals/role outputs; the TUI does not implement business transitions or spin up agents.

- Commit human approval and its corresponding frozen content/version and dispatch eligibility **atomically** in SQLite. Dispatch still belongs to the Go supervisor. Keep the unique `idea_id` separate from the shared source ideation `run_id`. Retain the **already specified** distinct execution/attempt IDs, heartbeats, ownership leases/fencing and crash recovery for every agent role; do not invent redundant ID systems.

- **Do not introduce a new generalized periodic-watcher design as a standalone requirement** based on the recent discussion: the user declined that suggestion. The proposed new `execution_runs` table is also explicitly excluded from this plan; do not add it by inference. Existing explicitly specified approved-idea and approved-plan supervisor watchers remain in scope unless separately changed; do not silently remove those earlier requirements. Existing durability requirements remain, with the supporting physical schema requiring reconciliation rather than an implicitly approved new table.

## Implementation readiness and acceptance

This document specifies required behavior, not proof that the implementation or third-party capabilities have passed validation. Preserve the approved scope while closing the decisions below. Do not describe the complete implementation handoff as unblocked until those decisions and the required compatibility checks are resolved.

### Implementation contracts

- **Approval binds bytes, not a filename:** Resolve and read the plan document through the supervisor's authorized output path, validate nonempty unique requirement IDs and their evidence methods, and persist the immutable approved bytes/hash with the approval transaction. Coding and Reviewer consume that version, not whatever later happens to occupy `plan_path`. Editing a draft requires validation again; editing an approved file cannot change the active workflow. A changed scope needs a new approval and explicit handling of any earlier active version.

- **Cancellation versus completion:** Both successful completion and cancellation commit only against the current ownership generation and expected workflow state. If cancellation commits first, late success cannot publish a plan. If successful completion commits first, report that planning already completed rather than converting a valid completed idea to a cancellation error. Stop/fence the cancelled writer before replacement. Repeated commands return the existing outcome rather than causing duplicate transitions.

- **File/database recovery:** A Markdown file write and SQLite commit are not one atomic transaction. Persist the attempt/output identity before writing, write and verify the intended file before recording completion, and reconcile that same identity after interruption. Do not mark a plan ready on a missing, empty, unreadable, or mismatched artifact; do not overwrite another attempt's file. The same reconciliation principle applies to local finalization: record and verify the accepted tree and delivery commit before claiming success, and recover an already-created matching commit rather than committing again.

- **Novelty consistency:** Scope history to project plus direction across every status and archives. Include accepted current-run candidates, exclude only the current idea when editing it, and serialize the final comparison/publication for competing writes to that scope. Recheck against newly committed ideas before publication. Persist the consumed generation budget before generation so crashes cannot reset the five-candidate limit. Exact duplicates consume an attempt as well. An empty matching history is novel; vector/model errors are infrastructure errors, never evidence of uniqueness.

- **Verification evidence:** Freeze the repository baseline, candidate, approved plan, command configuration, scanner versions/rules and quality comparison identity together. Run baseline and candidate comparisons under compatible settings; missing or incomparable baseline evidence is an error, not a clean result. Every newly introduced smell/error fails regardless of severity. An autofix or any other source change invalidates the previous candidate's acceptance and requires every mandatory check plus review again. The generic non-LLM runner uses its approved per-command timeouts; the five-minute ACP-attempt timeout is not a replacement for those command timeouts.

- **Review/idle time:** Launching Coding and Reviewer side by side does not require a Reviewer model to spend its five-minute execution budget waiting for code. Persist readiness/preparation and end that attempt when preparation finishes; start a new review attempt when immutable candidate evidence is ready. Durable waiting is not an active model attempt. Keep the existing uncapped correction loop and bounded recovery policy separate.

- **Read-only access and Git:** Supervisor-controlled worktree setup and trusted artifact/commit operations must enforce agent boundaries even where Git worktrees share repository metadata. A read-only agent cannot mutate refs, other worktrees, hooks or shared Git configuration through its Git directory. Finalization may create only the authorized local delivery commit from the accepted tree; no push, merge or PR side effects. Test these restrictions rather than treating separate directories as isolation.

### Decisions still needed

1. **Physical schema and migration:** Reconcile `planner_runs` with the five-column `plans` table and define supporting records for versions, links, claims, archives, vectors and checkpoints, without adding the declined `execution_runs` table or extra core-table columns. Specify keys/uniqueness, nullability, foreign-key deletion behavior, indexes, and a lossless versioned migration from existing data. Keep the existing operational `completed` status distinct from domain `complete`.

2. **Domain lifecycle meaning:** Define when `plans.status` becomes `complete` and what each project status means, including error/retry transitions. Recommended plan boundary: successful local finalization, not planning completion, review alone or push. This recommendation is not an approved state transition. Resolve whether reapproving a changed plan cancels or waits for an earlier active version, and whether more than one plan per idea is allowed.

3. **Field semantics:** Confirm whether to enforce the four `idea_type` values with a database `CHECK`. Specify the timestamp meaning, storage format/time zone and immutability for each table; choose one consistent convention rather than silently mixing creation and update times. Specify `run_id` format and source record, canonical UUID formatting and required/empty-value validation for all remaining core fields.

4. **Embedding compatibility:** Select the embedding model/runtime and its permitted ACP-compatible access, dimensions, canonical input serialization, fingerprint normalization and similarity conversion for the installed `sqlite-vec` version. Define model/version migration and re-embedding behavior; do not compare incompatible vectors. The configured v1 cutoff remains strictly below 0.60 and uses no LLM novelty judge.

5. **Runtime compatibility and limits:** Validate the three ACP profiles, model/thinking selection, nono egress/filesystem enforcement on supported platforms, and the exact Qlty/scanner invocation and untracked-file behavior. Specify poll/concurrency limits, total retrieval budget, verification-runner deployment/transport and an enforcement-tested credential/network policy. These are validation/design blockers where unsupported, not permission to bypass the existing restrictions.

6. **Quality comparison:** Choose the baseline revision, finding identity/deduplication method and handling of moved files/rule-version changes before claiming reliable “no new findings” enforcement. Reconcile any local-changed-files scan mode with rules requiring broader analysis. Mandatory scope must not be silently narrowed to make results pass.

7. **Revisits:** Decide whether explicit human reconsideration reuses an existing idea or creates an approved exception workflow. Until then, revisit labels do not override v1 novelty rejection.

### Required acceptance tests

- **Schema:** Reject null/invalid domain statuses and missing FK targets on every connection; retain shared run IDs across distinct ideas; preserve IDs, links, archives and status history through migration. Test exact approved table column sets.

- **Approvals:** Draft-only edits race safely with approval. Repeated approval/cancel requests and two concurrent supervisors produce one durable transition/workflow. Verify completed or paused approved versions are not newly dispatched. A changed file at the same path does not alter approved bytes.

- **Planning:** Crash before/after claim, file creation and completion transaction; recover without duplicate files/plans or false completion. Exercise both orderings of cancel versus completion and reject stale-owner results. Recoverable attempt failure must not prematurely terminate the idea.

- **Novelty:** Test 0.5999, 0.60 and above; exact duplicates, no history, archived history, cross-project/direction exclusion, self-exclusion during edits, concurrency, vector failure, edited drafts, persisted five-attempt budget and partial success. Do not test deferred external-implementation detection as a required v1 feature.

- **Review/quality:** Exactly 80% fails; greater than 80% passes only with every mandatory gate. Fail missing/unexecuted checks and new non-blocking smells. Keep pre-existing findings. Run later commands after an earlier failure. Invalidate acceptance after autofix or candidate/configuration change. Empty requirement sets fail validation.

- **Recovery/finalization:** Exercise every role, expired leases, strict 300-second deadlines, three same-entry restarts, fallback exhaustion, TUI closure, waiting for human input, stale writers and interrupted local commit creation. Assert preserved budgets/checkpoints, no duplicate effects and no push/PR/merge.

- **Integration/isolation:** Prove ACP effective settings and fail-closed sandbox enforcement, protected shared Git metadata, scanner artifact isolation, full candidate fidelity including added/deleted/untracked files, and concurrent plans on distinct delivery branches. Retain evidence and exact tested component versions.




## Future Things to Do

- **Detect features implemented outside the ideas registry:** Before showing an idea, check current repository code and relevant commits or merged changes for features someone has already implemented, even if no original idea was logged in SQLite. Avoid recommending already-existing functionality. Record code evidence supporting a rejection. This is a future enhancement, not part of the v1 novelty gate.

- **Three-band semantic novelty gate (future enhancement):** Replace the v1 single 0.60 cutoff with cosine-similarity bands using the closest prior ideas from the **same project AND direction**. **Below 0.50:** treat as unique. **0.50 through 0.75 inclusive:** perform a bounded LLM comparison of the candidate against its nearest historical matches to decide whether it is genuinely unique or a duplicate. **Above 0.75:** treat as similar and regenerate without an LLM call. Preserve the five-candidate limit unless deliberately changed later. Calibrate thresholds using the selected embedding model and measured false matches. This is a future design only; v1 remains strictly below 0.60 = unique, otherwise regenerate, with no LLM novelty judge.
