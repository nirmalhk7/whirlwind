# Personal and Agentic workflows

Whirlwind uses the same implementation, verification, review, and submission pipeline in both modes. Missing `mode` means `personal`.

```yaml
mode: personal
```

Change mode by editing the launched `settings.yaml` **on disk**, then restart Whirlwind. The UI displays the active mode and a pending restart; neither settings APIs nor the UI YAML editor can change mode. Other settings still reload normally.

A restart into a different mode permanently cancels incomplete work. It preserves worktrees, diffs, plans, history, and existing pull requests. Cancelled tasks cannot resume or be revived by unchanged source events. A normal restart in the same mode keeps recovery behavior.

## Personal mode

Ideation reads GitHub issues/discussions through `gh` and a deterministic TODO/FIXME scan of repository source. Selecting an idea starts planning. The specification describes what changes, why, expected benefit, affected paths, acceptance criteria, implementation, and verification.

Planner publishes the specification as an issue tagged `whirlwind:ready`. An open issue with that label directly authorizes implementation; no second inbox selection is required. Issue description remains authoritative. Removing the label or closing the issue stops unfinished work; edits invalidate affected execution evidence.

## Agentic mode

Ideation discovers every connected integration exposed through its configured provider's local API. Repository-scoped sources include external issues, CI results, documentation, and discussions. Source entries below supply scope overrides; they are not a whitelist.

```yaml
mode: agentic
integrations:
  discovery: connected
  sources:
    - provider: codex
      ref: app://YOUR_NATIVE_INTEGRATION_ID
      scopes:
        - YOUR_EXTERNAL_PROJECT
  publication:
    https://your-git-host.example/team/repository:
      provider: codex
      ref: app://YOUR_NATIVE_PR_INTEGRATION_ID
```

Use Settings to inspect provider IDs, native connection references, and callable state. Refresh explicitly tests the native connection. Register each repository's canonical HTTPS origin as a publication key. SSH origins normalize to HTTPS without `.git`.

A connection must be available through the provider executing that operation. Whirlwind errors on provider mismatch; it never switches providers to access someone else's connection or falls back to `gh`. GUI-only connections unavailable through the local API need reconnecting through supported native MCP configuration. Cursor ACP does not expose dashboard-only team MCP connections.

Authentication stays in provider-native storage. Whirlwind provisions bounded native configuration into its isolated runtime, retaining project sandbox/network restrictions. Configure required domains, environment references, and command dependencies in confirmed execution policy; connecting an integration does not expand that policy.

Plans are saved at `~/.whirlwind/plans/<project-id>/<task-id>/v<N>.md`. Inbox offers approval, revision feedback, or rejection. Documents can be edited locally; the UI shows the exact current version. Approval binds file hash, version, and project policy. Subsequent edits revoke approval and stop implementation. Approved plan snapshots remain immutable.

Agentic mode does not require GitHub issues, GitHub authentication, or the `gh` executable. Local Git handles worktrees, fetch, commits, and push. Configured native integration handles repository metadata, PR creation/lookup, and outcome monitoring.

Publication requires one effective `origin` push URL matching that canonical repository. A separate fork `pushurl`, multiple destinations, or a changed origin blocks publication before pushing. Native PR lookup and creation must use the tools validated during destination preflight; unrelated tool calls cannot authorize publication.

## Coding, quality, review, and submission

Coding uses isolated worktrees and red-green-refactor TDD. Use host `run_check` with an exact committed check ID to record failing regression and passing implementation evidence. Crashes, timeouts, and opaque failures are not TDD evidence. Questions and permissions appear in Inbox.

Verification compares full configured checks against a pinned base snapshot. Existing debt remains visible; new or worsened failures and Qlty findings block publication. Qlty requires structured SARIF; failing tests need structured Go JSON or JUnit results to distinguish existing failures reliably. Opaque failing checks block rather than being grandfathered.

When unrelated tests already fail, TDD green requires every newly failing regression test to explicitly pass and only baseline failures to remain. Host evidence retains the raw failing check result and explains baseline acceptance; equal exit codes alone never establish a passing gate.

Reviewer checks the full diff against the approved specification and recorded evidence. Repairs repeat verification and review.

Submitter is separately configurable:

```yaml
agents:
  submit:
    - provider: codex
      model: YOUR_CONFIGURED_MODEL
```

Without an explicit Submitter chain, coding choices are inherited. Submitter proposes meaningful commits and human-readable PR text. Host validates that commit groups cover the complete accepted diff, creates a separate submission branch, and requires exact Git-tree equality with reviewed code. Internal checkpoints remain recoverable. Final checks and reviewer confirmation run against final commits before critical-change publication approval.

Personal PRs use `gh` and reference the resolved issue. Agentic PRs use the selected native integration and link existing source issues or local plan identity. Publication retries reconcile existing PRs before creating another. Whirlwind does not merge automatically.

## Opt-in live connection checks

Automated fixtures never use live accounts or create real pull requests. After configuring native connections, use **Settings → Refresh integrations** to test each provider's metadata API. Registration and coding preflight verify the explicit destination exposes PR lookup and creation tools through that same provider.

For read-only live tests against your configured providers and destinations:

```sh
WHIRLWIND_LIVE_INTEGRATIONS=1 \
WHIRLWIND_LIVE_SETTINGS=/absolute/path/settings.yaml \
WHIRLWIND_LIVE_REPOSITORIES='{"https://git.example/team/repo":"/absolute/path/checkout"}' \
go test ./internal/workflow -run '^TestLiveNativeConnections$' -v
```

Provide a local checkout with committed execution policy for every publication destination. Native network and environment permissions must already be configured. These opt-in checks inspect metadata and capabilities; they do not publish PRs. Default test runs skip live checks.

Read-only API routes include `GET /api/v1/workflow_config/global`, `GET /api/v1/providers/{id}/integrations`, and `GET /api/v1/tasks/{id}/plan-document`. Explicit refresh uses `POST /api/v1/providers/{id}/integrations`; plan decisions use `POST /api/v1/tasks/{id}/plan-review` with the current task version and document hash. See [OpenAPI](openapi.json) for request contracts.
