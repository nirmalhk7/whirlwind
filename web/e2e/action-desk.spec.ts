import { expect, test, type Page, type Route } from "@playwright/test";

type PlanContent = {
  objective: string;
  acceptance_criteria: string[];
  allowed_paths: string[];
  checks: string[];
  workflow: "single" | "pipeline";
  unresolved_requirements: string[];
};

function planFixture(unresolved = ["What should count as complete?"]): { plan: any; submissions: any[]; deferredPlans: any[] } {
  const submissions: any[] = [];
  const deferredPlans: any[] = [];
  return {
    submissions,
    deferredPlans,
    plan: {
      id: "smoke-task",
      type: "plan",
      project: "smoke-project",
      status: "draft",
      summary: "Draft plan: make the Action Desk clearer",
      updated_at: "2026-10-11T00:00:00Z",
      record: {
        version: 1,
        content: {
          objective: "Make the Action Desk clearer",
          acceptance_criteria: ["All decisions stay on the Action Desk"],
          allowed_paths: ["web/app/page.tsx"],
          checks: [],
          workflow: "pipeline",
          unresolved_requirements: unresolved,
        } satisfies PlanContent,
      },
      evidence: { plan: { version: 1, content: { objective: "Make the Action Desk clearer" } } },
    },
  };
}

async function mockDeskApi(
  page: Page,
  fixture: ReturnType<typeof planFixture>,
  editStatus = 200,
  inboxItems?: any[],
) {
  let latestDiscoveryScan: any = null;
  let proposals: any[] = [];
  let campaigns: any[] = [];
  let campaignId = "";
  await page.route("**/api/v1/**", async (route: Route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    if (path === "/api/v1/events") {
      await route.fulfill({ status: 204 });
      return;
    }
    if (path === "/api/v1/overview") {
      await route.fulfill({ json: { metrics: {}, active: [] } });
      return;
    }
    if (path === "/api/v1/projects") {
      await route.fulfill({ json: [{
        name: "smoke-project", validate_commands: [], priorities: [], exclusions: [],
        protected_paths: [], workspace_mode: "worktree", sandbox: "read-only", approval_policy: "on-request",
        latest_discovery_scan: latestDiscoveryScan,
      }] });
      return;
    }
    if (path === "/api/v1/projects/smoke-project/discovery-scans" && request.method() === "POST") {
      fixture.submissions.push({ kind: "discovery.scan", body: request.postDataJSON() });
      latestDiscoveryScan = {
        id: "discovery-scan-1", project: "smoke-project", trigger: "manual", status: "succeeded",
        head_commit: "abc123", direction: { priorities: ["reliability"], exclusions: [] },
        evidence: [
          { kind: "todo", path: "src/fragile.py", line: 4, title: "TODO marker" },
          { kind: "file_change", path: "src/pending.py", line: null, title: "Working-tree change" },
        ],
        evidence_digest: "digest-1", direction_digest: "direction-1", evidence_changed: true,
        scout_recommended: true, created_at: "2026-10-11T00:00:00Z",
      };
      await route.fulfill({ json: latestDiscoveryScan });
      return;
    }
    if (path === "/api/v1/projects/smoke-project/discovery-proposals" && request.method() === "GET") {
      await route.fulfill({ json: proposals });
      return;
    }
    if (path === "/api/v1/projects/smoke-project/discovery-proposals" && request.method() === "POST") {
      fixture.submissions.push({ kind: "proposal.synthesize", body: request.postDataJSON() });
      proposals = [{ id: "proposal-1", project: "smoke-project", version: 1, status: "proposed",
        category: "reliability", problem: "A TODO remains in src/fragile.py", benefit: "Clarify reliability behavior.",
        impact: "low", confidence: "low", uncertainty: ["Confirm relevance."], scope: ["src/fragile.py"],
        risks: [], dependencies: [], acceptance: ["Confirm the marker is still relevant."],
        recommended_next_action: "Review evidence.", evidence: [{ kind: "todo", path: "src/fragile.py", title: "TODO marker", line: 4 }] }];
      await route.fulfill({ json: proposals.map((proposal) => ({ ...proposal, inserted: true })) });
      return;
    }
    if (path === "/api/v1/discovery-proposals/proposal-1" && request.method() === "PATCH") {
      const body = request.postDataJSON();
      fixture.submissions.push({ kind: "proposal.revise", body });
      proposals = proposals.map((proposal) => proposal.id === "proposal-1"
        ? { ...proposal, ...body.changes, version: body.expected_version + 1 } : proposal);
      await route.fulfill({ json: proposals[0] });
      return;
    }
    if (path === "/api/v1/discovery-proposals/proposal-1/history" && request.method() === "GET") {
      await route.fulfill({ json: proposals });
      return;
    }
    if (path === "/api/v1/discovery-proposals/proposal-1/approvals" && request.method() === "POST") {
      const body = request.postDataJSON();
      fixture.submissions.push({ kind: "proposal.approve", body, key: request.headers()["idempotency-key"] });
      proposals = proposals.map((proposal) => ({ ...proposal, status: "approved", version: body.expected_version + 1,
        planning_task_id: "planning-task-1" }));
      await route.fulfill({ json: { proposal: proposals[0], operation: { state: "succeeded" } } });
      return;
    }
    if (path === "/api/v1/projects/smoke-project/campaigns" && request.method() === "GET") {
      await route.fulfill({ json: campaigns }); return;
    }
    if (path === "/api/v1/projects/smoke-project/campaigns" && request.method() === "POST") {
      const body = request.postDataJSON(); fixture.submissions.push({ kind: "campaign.create", body }); campaignId = body.id;
      campaigns = [body]; await route.fulfill({ json: body }); return;
    }
    if (path === `/api/v1/campaigns/${campaignId}/approvals` && request.method() === "POST") {
      const body = request.postDataJSON(); fixture.submissions.push({ kind: "campaign.approve", body });
      campaigns = campaigns.map((campaign) => ({ ...campaign, status: "approved", version: body.expected_version + 1,
        approved_digest: "a".repeat(64) }));
      await route.fulfill({ json: campaigns[0] }); return;
    }
    if (path === `/api/v1/campaigns/${campaignId}/history` && request.method() === "GET") {
      await route.fulfill({ json: campaigns }); return;
    }
    if (path === `/api/v1/campaigns/${campaignId}/progress` && request.method() === "GET") {
      await route.fulfill({ json: { campaign_id: campaignId, version: campaigns[0].version, action: "ready",
        step_id: "step-1", reason: "All declared dependencies reached their required outcomes." } }); return;
    }
    if (path === "/api/v1/settings") {
      await route.fulfill({ json: {
        revision: "smoke-revision",
        global: { codex_bin: "codex", claude_bin: "claude", cursor_agent_bin: "cursor-agent",
          codex_model: "gpt-test", codex_reasoning_effort: "high", opencode_enabled: false,
          opencode_model: null, agent_runtime_timeout_seconds: 2700, agent_idle_timeout_seconds: 600,
          max_concurrent_runs: 3 },
        profiles: {},
        projects: [{ name: "smoke-project", validate_commands: [], priorities: [], exclusions: [],
          protected_paths: [], workspace_mode: "worktree", sandbox: "read-only", approval_policy: "on-request" }],
      } });
      return;
    }
    if (path === "/api/v1/providers") {
      await route.fulfill({ json: [] });
      return;
    }
    if (path === "/api/v1/execution-plans/deferred") {
      await route.fulfill({ json: fixture.deferredPlans });
      return;
    }
    if (path === "/api/v1/inbox") {
      const rows = inboxItems ?? (fixture.plan.status === "draft" ? [{
        run_id: "smoke-task", subject_type: "plan", classification: "decision_required",
        summary: fixture.plan.summary, evidence: ["Draft plan v" + fixture.plan.record.version],
        recommended_actions: ["Review the plan"],
      }] : []);
      const offset = Number(url.searchParams.get("offset") ?? "0");
      await route.fulfill({ json: rows.slice(offset, offset + 200) });
      return;
    }
    if (path === "/api/v1/records/smoke-task" && request.method() === "GET") {
      await route.fulfill({ json: fixture.plan });
      return;
    }
    if (path === "/api/v1/execution-plans/smoke-task/deferrals" && request.method() === "POST") {
      const body = request.postDataJSON();
      fixture.submissions.push({ kind: "defer", body, key: request.headers()["idempotency-key"] });
      fixture.plan.status = "deferred";
      fixture.deferredPlans.push({ task_id: "smoke-task", project: "smoke-project", title: "Draft plan",
        version: body.version, objective: fixture.plan.record.content.objective, updated_at: "2026-10-11T00:01:00Z" });
      await route.fulfill({ json: { state: "succeeded", id: "op-defer" } });
      return;
    }
    if (path === "/api/v1/execution-plans/smoke-task/restorations" && request.method() === "POST") {
      const body = request.postDataJSON();
      fixture.submissions.push({ kind: "restore", body, key: request.headers()["idempotency-key"] });
      fixture.plan.status = "draft";
      fixture.deferredPlans.splice(0);
      await route.fulfill({ json: { state: "succeeded", id: "op-restore" } });
      return;
    }
    if (path === "/api/v1/execution-plans/smoke-task/answers" && request.method() === "POST") {
      const body = request.postDataJSON();
      fixture.submissions.push({ kind: "answer", body });
      fixture.plan.record.version = body.version + 1;
      fixture.plan.record.content.unresolved_requirements = [];
      fixture.plan.evidence = { plan: { version: fixture.plan.record.version, content: fixture.plan.record.content } };
      await route.fulfill({ json: { state: "succeeded" } });
      return;
    }
    if (path === "/api/v1/execution-plans/smoke-task/edits" && request.method() === "POST") {
      const body = request.postDataJSON();
      fixture.submissions.push({ kind: "edit", body });
      if (editStatus !== 200) {
        if (fixture.plan.record.version <= body.version) {
          fixture.plan.record.version = body.version + 1;
          fixture.plan.record.content.objective = "Current objective from another reviewer";
          fixture.plan.summary = "Plan changed elsewhere";
          fixture.plan.updated_at = "2026-10-11T00:05:00Z";
          fixture.plan.evidence = { plan: { version: fixture.plan.record.version, content: fixture.plan.record.content } };
        }
        await route.fulfill({ status: editStatus, json: { error: { message: "Execution plan version is stale." } } });
        return;
      }
      fixture.plan.record.version = body.version + 1;
      fixture.plan.record.content = { ...fixture.plan.record.content, ...body, unresolved_requirements: [] };
      fixture.plan.evidence = { plan: { version: fixture.plan.record.version, content: fixture.plan.record.content } };
      await route.fulfill({ json: { state: "succeeded" } });
      return;
    }
    await route.fulfill({ status: 404, json: { error: { message: `Unexpected smoke-test request: ${path}` } } });
  });
}

test("manual discovery scan is reviewable from the single-screen Action Desk", async ({ page }) => {
  const fixture = planFixture([]);
  await mockDeskApi(page, fixture, 200, []);
  await page.goto("/");

  await page.getByRole("button", { name: "Scan project now" }).click();

  await expect(page.getByRole("status").filter({ hasText: "Discovery scan completed" })).toBeVisible();
  await expect(page.getByText("src/fragile.py:4 · TODO marker")).toBeVisible();
  await expect(page.getByText("src/pending.py · Working-tree change")).toBeVisible();
  await expect(page).toHaveURL(/\/$/);
  expect(fixture.submissions).toContainEqual({ kind: "discovery.scan", body: { trigger: "manual" } });
  await page.reload();
  await expect(page.getByText("src/fragile.py:4 · TODO marker")).toBeVisible();
  await expect(page.getByText("src/pending.py · Working-tree change")).toBeVisible();
});

test("discovery proposals can be synthesized, revised, and approved into Planning from the Action Desk", async ({ page }) => {
  const fixture = planFixture([]);
  await mockDeskApi(page, fixture, 200, []);
  await page.goto("/");

  await page.getByRole("button", { name: "Synthesize from latest scan" }).click();
  const proposal = page.getByRole("region", { name: "Discovery proposals" }).locator("article");
  await expect(proposal.getByText("A TODO remains in src/fragile.py")).toBeVisible();
  await proposal.getByText("Edit proposal").click();
  await proposal.getByLabel("Problem").fill("Confirm the client reliability TODO.");
  await proposal.getByRole("button", { name: "Save revision" }).click();
  await expect(page.getByText("Proposal edited and saved as a new version.")).toBeVisible();
  await proposal.getByRole("button", { name: "Show proposal history" }).click();
  await expect(proposal.getByRole("list", { name: "History for proposal proposal-1" }).getByText(/v2 · proposed/)).toBeVisible();
  await proposal.getByRole("button", { name: "Approve to Planning" }).click();
  await expect(page.getByText("Planning draft created for task planning-task-1.")).toBeVisible();
  expect(fixture.submissions.map((submission) => submission.kind)).toContain("proposal.synthesize");
  expect(fixture.submissions.map((submission) => submission.kind)).toContain("proposal.revise");
  expect(fixture.submissions.map((submission) => submission.kind)).toContain("proposal.approve");
  expect(fixture.submissions.find((submission) => submission.kind === "proposal.approve").key).toBeTruthy();
});

test("campaign contracts can be drafted and approved without starting work", async ({ page }) => {
  const fixture = planFixture([]);
  await mockDeskApi(page, fixture, 200, []);
  await page.goto("/");
  await page.getByLabel("Campaign objective").fill("Improve import reliability");
  await page.getByLabel("Acceptance criteria, one per line").fill("Import errors remain typed");
  await page.getByRole("button", { name: "Save campaign draft" }).click();
  const campaign = page.getByRole("region", { name: "Project campaigns" }).locator("article");
  await expect(campaign.getByText("Improve import reliability", { exact: true })).toBeVisible();
  await campaign.getByRole("button", { name: "Approve contract" }).click();
  await expect(page.getByText(/Progression remains disabled until its execution and completion gates/)).toBeVisible();
  await campaign.getByRole("button", { name: "Show campaign history" }).click();
  await expect(campaign.getByRole("list", { name: /History for campaign/ }).getByText(/v2 · approved/)).toBeVisible();
  await campaign.getByRole("button", { name: "Evaluate next step" }).click();
  await expect(campaign.getByRole("status").filter({ hasText: "step step-1" })).toBeVisible();
  expect(fixture.submissions.map((submission) => submission.kind)).toContain("campaign.create");
  expect(fixture.submissions.map((submission) => submission.kind)).toContain("campaign.approve");
});

test("all unresolved Action Desk cards appear in the aggregate across inbox pages", async ({ page }) => {
  const fixture = planFixture([]);
  const inboxRequests: number[] = [];
  const inboxItems = Array.from({ length: 201 }, (_, index) => ({
    run_id: `needs-human-${index}`,
    subject_type: "run",
    classification: "decision_required",
    summary: `Run ${index} needs your decision`,
    evidence: ["status=needs_human"],
    recommended_actions: ["Review the run"],
  }));
  await mockDeskApi(page, fixture, 200, inboxItems);
  page.on("request", (request) => {
    const url = new URL(request.url());
    if (url.pathname === "/api/v1/inbox") {
      inboxRequests.push(Number(url.searchParams.get("offset") ?? "0"));
    }
  });
  await page.goto("/");

  await expect(page.getByText("201 open items")).toBeVisible();
  await expect(page.locator(".action-card")).toHaveCount(201);
  expect(new Set(inboxRequests)).toEqual(new Set([0, 200]));
  await expect(page).toHaveURL(/\/$/);
});

test("Codex permission denial is submitted and tracked from the Action Desk drawer", async ({ page }) => {
  const record: any = {
    id: "permission-1", type: "permission", project: "Codex request", status: "pending",
    summary: "Codex permission request needs a decision", updated_at: "2026-10-11T00:00:00Z",
    record: { method: "item/commandExecution/requestApproval", request_id: 17, revision: 1,
      status: "pending", params: { command: "pytest --version", cwd: "/workspace/smoke", reason: "Run verification" } },
    evidence: {},
  };
  const submissions: Array<{ body: any; key: string | null }> = [];
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    if (path === "/api/v1/events") return route.fulfill({ status: 204 });
    if (path === "/api/v1/overview") return route.fulfill({ json: { metrics: {}, active: [] } });
    if (path === "/api/v1/projects") return route.fulfill({ json: [{
      name: "smoke-project", validate_commands: [], priorities: [], exclusions: [], protected_paths: [],
      workspace_mode: "worktree", sandbox: "read-only", approval_policy: "on-request",
    }] });
    if (path === "/api/v1/settings") return route.fulfill({ json: { revision: "smoke-revision",
      global: { codex_bin: "codex", claude_bin: "claude", cursor_agent_bin: "cursor-agent",
        codex_model: "gpt-test", codex_reasoning_effort: "high", opencode_enabled: false,
        opencode_model: null, agent_runtime_timeout_seconds: 2700, agent_idle_timeout_seconds: 600,
        max_concurrent_runs: 3 }, profiles: {}, projects: [] } });
    if (path === "/api/v1/execution-plans/deferred") return route.fulfill({ json: [] });
    if (path === "/api/v1/inbox") return route.fulfill({ json: [{
      run_id: record.id, subject_type: "permission", classification: "decision_required",
      summary: record.summary, evidence: ["provider=codex", "request=commandExecution/requestApproval"],
      recommended_actions: ["Decline this request"],
    }] });
    if (path === "/api/v1/permissions/permission-1" && request.method() === "GET") {
      return route.fulfill({ json: record });
    }
    if (path === "/api/v1/permissions/permission-1/decisions" && request.method() === "POST") {
      submissions.push({ body: request.postDataJSON(), key: request.headers()["idempotency-key"] ?? null });
      record.status = "response_queued";
      record.record.status = "response_queued";
      record.record.revision = 2;
      return route.fulfill({ status: 202, json: { id: "permission-op-1", state: "accepted" } });
    }
    return route.fulfill({ status: 404, json: { error: { message: `Unexpected smoke-test request: ${path}` } } });
  });
  await page.goto("/");

  await page.getByRole("button", { name: "Review evidence and act" }).click();
  await expect(page.getByText("pytest --version", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Decline request" }).click();
  await expect(page.getByText("Waiting for Codex to confirm the permission decision", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Grant permission" })).toHaveCount(0);
  expect(submissions).toHaveLength(1);
  expect(submissions[0].body).toMatchObject({ decision: "decline", expected_revision: 1 });
  expect(submissions[0].key).toBeTruthy();
  await expect(page).toHaveURL(/\/$/);
});

test("GitHub sync and local follow-up stay on the Action Desk", async ({ page }) => {
  let enabled = false;
  let synced = false;
  let revision = "github-revision-1";
  let syncState: any = null;
  const externalCalls: string[] = [];
  const githubItem: any = {
    id: "github-item-1", project: "smoke-project", external_id: "PR_kwDOPull1",
    kind: "pull_request", number: 8, title: "Fix session refresh",
    state: "OPEN", url: "https://github.com/acme/demo/pull/8",
    body: "Refresh expired sessions safely.", labels: ["bug"],
    updated_at: "2026-10-11T00:00:00Z", synced_at: "2026-10-11T00:01:00Z",
    blockers: ["CI check failed: unit"], review_decision: "CHANGES_REQUESTED",
    status_check_rollup: [{ name: "unit", status: "COMPLETED", conclusion: "FAILURE" }],
  };
  const githubCard = {
    run_id: githubItem.id, subject_type: "github", classification: "decision_required",
    score: 104, summary: "GitHub PR #8: Fix session refresh · CI check failed: unit",
    evidence: ["project=smoke-project", `url=${githubItem.url}`, "blocker=CI check failed: unit"],
    recommended_actions: ["Review the GitHub evidence and prepare a local plan draft on the Action Desk"],
    primary_action: "draft_github_followup",
  };
  const project = () => ({ name: "smoke-project", validate_commands: [], priorities: [], exclusions: [],
    protected_paths: [], workspace_mode: "worktree", sandbox: "read-only", approval_policy: "on-request",
    github_sync_enabled: enabled, github_sync: syncState });
  const settings = () => ({ revision, global: { codex_bin: "codex", claude_bin: "claude", cursor_agent_bin: "cursor-agent",
    codex_model: "gpt-test", codex_reasoning_effort: "high", opencode_enabled: false, opencode_model: null,
    agent_runtime_timeout_seconds: 2700, agent_idle_timeout_seconds: 600, max_concurrent_runs: 3 },
    profiles: {}, projects: [project()] });

  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    if (path === "/api/v1/events") return route.fulfill({ status: 204 });
    if (path === "/api/v1/overview") return route.fulfill({ json: { metrics: {}, active: [] } });
    if (path === "/api/v1/projects") return route.fulfill({ json: [project()] });
    if (path === "/api/v1/settings" && request.method() === "GET") return route.fulfill({ json: settings() });
    if (path === "/api/v1/settings" && request.method() === "PATCH") return route.fulfill({ json: { state: "succeeded" } });
    if (path === "/api/v1/providers") return route.fulfill({ json: [] });
    if (path === "/api/v1/execution-plans/deferred") return route.fulfill({ json: [] });
    if (path === "/api/v1/inbox") return route.fulfill({ json: synced ? [githubCard] : [] });
    if (path === "/api/v1/projects/smoke-project" && request.method() === "PATCH") {
      externalCalls.push("project-capability-save");
      const body = request.postDataJSON();
      enabled = body.changes.github_sync_enabled;
      revision = "github-revision-2";
      return route.fulfill({ json: { state: "succeeded", result: revision } });
    }
    if (path === "/api/v1/projects/smoke-project/github-sync" && request.method() === "POST") {
      externalCalls.push("github-read-sync");
      synced = true;
      syncState = { synced_at: "2026-10-11T00:01:00Z", issue_count: 1, pull_request_count: 1 };
      return route.fulfill({ json: { project: "smoke-project", issue_count: 1, pull_request_count: 1,
        synced_at: syncState.synced_at, items: ["github-item-1"] } });
    }
    if (path === "/api/v1/records/github-item-1") return route.fulfill({ json: {
      ...githubItem, type: "github", summary: "GitHub pull request #8: Fix session refresh",
      record: githubItem, evidence: githubItem,
    } });
    return route.fulfill({ status: 404, json: { error: { message: `Unexpected smoke-test request: ${path}` } } });
  });

  await page.goto("/");
  const capability = page.getByRole("checkbox", { name: /Allow Whirlwind to read open issues/ });
  await expect(capability).not.toBeChecked();
  await expect(page.getByRole("button", { name: "Sync GitHub now" })).toBeDisabled();
  await capability.check();
  await page.getByRole("button", { name: "Save project direction and policy" }).click();
  await expect(page.getByRole("button", { name: "Sync GitHub now" })).toBeEnabled();
  await page.getByRole("button", { name: "Sync GitHub now" }).click();
  await expect(page.getByText(/Read 1 open issues and 1 open pull requests/)).toBeVisible();
  await expect(page.getByText(/Last complete sync/)).toBeVisible();

  await page.getByRole("button", { name: "Review evidence and act" }).click();
  await expect(page.getByRole("dialog").getByText("CI check failed: unit").first()).toBeVisible();
  await page.getByRole("button", { name: "Copy evidence into a local proposal" }).click();
  const proposal = page.getByLabel("Requested outcome");
  await expect(proposal).toHaveValue(/github pull request #8/i);
  await expect(proposal).toHaveValue(/untrusted external data/i);
  await expect(proposal).toHaveValue(/https:\/\/github.com\/acme\/demo\/pull\/8/);
  await expect(page).toHaveURL(/\/$/);
  expect(externalCalls).toEqual(["project-capability-save", "github-read-sync"]);
});

test("GitHub issue import creates a local draft without leaving the Action Desk", async ({ page }) => {
  let importedPlan = false;
  let importedSourceUpdatedAt = "2026-10-11T00:00:00Z";
  let currentPlanVersion = 1;
  const issue: any = {
    id: "github-issue-card-7", type: "github", project: "smoke-project", status: "OPEN",
    updated_at: "2026-10-11T00:00:00Z", summary: "GitHub issue #7: Fix login",
    record: { id: "github-issue-card-7", project: "smoke-project", external_id: "I_kwDOIssue1",
      kind: "issue", number: 7, title: "Fix login", state: "OPEN",
      url: "https://github.com/acme/demo/issues/7", body: "Login fails on expired session.",
      labels: ["bug"], updated_at: "2026-10-11T00:00:00Z" },
    evidence: {},
  };
  const imports: Array<{ path: string; key: string | null }> = [];
  const refreshes: Array<{ body: any; key: string | null }> = [];
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path === "/api/v1/events") return route.fulfill({ status: 204 });
    if (path === "/api/v1/overview") return route.fulfill({ json: { metrics: {}, active: [] } });
    if (path === "/api/v1/projects") return route.fulfill({ json: [{ name: "smoke-project",
      validate_commands: ["pytest -q"], priorities: ["reliability"], exclusions: [], protected_paths: [],
      workspace_mode: "worktree", sandbox: "read-only", approval_policy: "on-request",
      github_sync_enabled: true, github_sync: { synced_at: issue.record.updated_at, issue_count: 1,
        pull_request_count: 0 } }] });
    if (path === "/api/v1/settings") return route.fulfill({ json: { revision: "smoke-revision",
      global: { codex_bin: "codex", claude_bin: "claude", cursor_agent_bin: "cursor-agent",
        codex_model: "gpt-test", codex_reasoning_effort: "high", opencode_enabled: false,
        opencode_model: null, agent_runtime_timeout_seconds: 2700, agent_idle_timeout_seconds: 600,
        max_concurrent_runs: 3 }, profiles: {}, projects: [] } });
    if (path === "/api/v1/execution-plans/deferred") return route.fulfill({ json: [] });
    if (path === "/api/v1/inbox") return route.fulfill({ json: [{ run_id: issue.id,
      subject_type: "github", classification: "watch", summary: issue.summary, score: 45,
      evidence: [`url=${issue.record.url}`], recommended_actions: ["Import this issue as a local plan"],
      primary_action: "import_github_issue" }] });
    if (path === `/api/v1/records/${issue.id}` && request.method() === "GET") return route.fulfill({ json: {
      ...issue, issue_import: importedPlan ? { task_id: "local-plan-1",
        source_updated_at: importedSourceUpdatedAt, source_changed: importedSourceUpdatedAt !== issue.record.updated_at,
        plan_version: currentPlanVersion, plan_status: "draft" } : null,
    } });
    if (path === "/api/v1/records/local-plan-1" && request.method() === "GET") return route.fulfill({ json: {
      id: "local-plan-1", type: "plan", project: "smoke-project", status: "draft",
      summary: "GH #7: Fix login", updated_at: issue.record.updated_at,
      plan_history: currentPlanVersion === 1 ? [{ version: 1, status: "draft",
        objective: "Prepare a local plan for GitHub issue #7", acceptance_criteria: [], updated_at: issue.record.updated_at }] : [
        { version: 1, status: "draft", objective: "Prepare a local plan for GitHub issue #7",
          acceptance_criteria: ["Expired sessions return 401 and refresh tokens are rotated."],
          updated_at: "2026-10-11T00:30:00Z",
          issue_source: { number: 7, updated_at: "2026-10-11T00:00:00Z" } },
        { version: 2, status: "draft", objective: "Prepare a refreshed local plan for GitHub issue #7",
          acceptance_criteria: [], updated_at: issue.record.updated_at }],
      record: { id: "plan-1", task_id: "local-plan-1", version: currentPlanVersion, status: "draft",
        content: { objective: currentPlanVersion === 1 ? "Prepare a local plan for GitHub issue #7" :
            "Prepare a refreshed local plan for GitHub issue #7",
          workflow: "pipeline", acceptance_criteria: [], allowed_paths: [], checks: ["pytest -q"],
          risk: "medium", unresolved_requirements: [currentPlanVersion === 1
            ? "Confirm issue acceptance criteria before approval."
            : "Review the updated issue source and confirm acceptance criteria."] } },
      evidence: {},
    } });
    if (path === "/api/v1/projects/smoke-project/github-issues/github-issue-card-7/imports"
        && request.method() === "POST") {
      imports.push({ path, key: request.headers()["idempotency-key"] ?? null });
      importedPlan = true;
      importedSourceUpdatedAt = issue.record.updated_at;
      issue.record.updated_at = "2026-10-11T01:00:00Z";
      issue.record.body = "Updated: expired sessions return 401.";
      issue.record.labels = ["security"];
      return route.fulfill({ status: 202, json: { task_id: "local-plan-1", plan_id: "plan-1",
        plan_version: 1, state: "draft", created: true, source_changed: false } });
    }
    if (path === "/api/v1/projects/smoke-project/github-issues/github-issue-card-7/refreshes"
        && request.method() === "POST") {
      refreshes.push({ body: request.postDataJSON(), key: request.headers()["idempotency-key"] ?? null });
      currentPlanVersion = 2;
      importedSourceUpdatedAt = issue.record.updated_at;
      return route.fulfill({ status: 202, json: { task_id: "local-plan-1", plan_id: "plan-2",
        plan_version: 2, state: "draft", created: true, source_changed: false } });
    }
    return route.fulfill({ status: 404, json: { error: { message: `Unexpected smoke-test request: ${path}` } } });
  });

  await page.goto("/");
  await page.getByRole("button", { name: "Review evidence and act" }).click();
  await expect(page.getByRole("button", { name: "Import issue as a local plan" })).toBeVisible();
  await page.getByRole("button", { name: "Import issue as a local plan" }).click();
  await expect(page.getByRole("status").getByText(/Created draft plan v1/)).toBeVisible();
  await expect(page.getByRole("status").getByText(/nothing was queued/)).toBeVisible();
  await expect(page.getByText(/changed after import/)).toBeVisible();
  await expect(page.getByRole("button", { name: "Create new plan version from latest issue" })).toBeVisible();
  await page.getByRole("button", { name: "Create new plan version from latest issue" }).click();
  await expect(page.getByRole("status").getByText(/Created plan v2 from the newer GitHub source/)).toBeVisible();
  await expect(page.getByRole("button", { name: "Open linked local plan" })).toBeVisible();
  await page.getByRole("button", { name: "Open linked local plan" }).click();
  await expect(page.getByText(/Review the updated issue source and confirm acceptance criteria/)).toBeVisible();
  await page.getByText("Earlier plan versions").click();
  await expect(page.getByText("Expired sessions return 401 and refresh tokens are rotated.")).toBeVisible();
  expect(imports).toHaveLength(1);
  expect(imports[0].key).toBe("github-issue-import:I_kwDOIssue1");
  expect(refreshes).toEqual([{ body: { expected_version: 1,
    expected_source_updated_at: "2026-10-11T01:00:00Z" },
    key: "github-issue-refresh:I_kwDOIssue1:2026-10-11T01:00:00Z:1" }]);
  await expect(page).toHaveURL(/\/$/);
});

test("Codex filesystem permission grants require explicit path review and last one turn", async ({ page }) => {
  const record: any = {
    id: "permission-fs-1", type: "permission", project: "Codex request", status: "pending",
    summary: "Codex permission request needs a decision", updated_at: "2026-10-11T00:00:00Z",
    record: { method: "item/permissions/requestApproval", request_id: 18, revision: 1, status: "pending",
      params: { reason: "Read the supplied fixture", grantable_filesystem: [
        { access: "read", path: "/tmp/fixtures/input" },
        { access: "write", path: "/tmp/fixtures/output" },
      ], network_requested: false } },
    evidence: {},
  };
  const submissions: Array<{ body: any; key: string | null }> = [];
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path === "/api/v1/events") return route.fulfill({ status: 204 });
    if (path === "/api/v1/overview") return route.fulfill({ json: { metrics: {}, active: [] } });
    if (path === "/api/v1/projects") return route.fulfill({ json: [{ name: "smoke-project",
      validate_commands: [], priorities: [], exclusions: [], protected_paths: [],
      workspace_mode: "worktree", sandbox: "read-only", approval_policy: "on-request" }] });
    if (path === "/api/v1/settings") return route.fulfill({ json: { revision: "smoke-revision",
      global: { codex_bin: "codex", claude_bin: "claude", cursor_agent_bin: "cursor-agent",
        codex_model: "gpt-test", codex_reasoning_effort: "high", opencode_enabled: false,
        opencode_model: null, agent_runtime_timeout_seconds: 2700, agent_idle_timeout_seconds: 600,
        max_concurrent_runs: 3 }, profiles: {}, projects: [] } });
    if (path === "/api/v1/execution-plans/deferred") return route.fulfill({ json: [] });
    if (path === "/api/v1/inbox") return route.fulfill({ json: [{ run_id: record.id,
      subject_type: "permission", classification: "decision_required", summary: record.summary,
      evidence: ["provider=codex", "request=permission"], recommended_actions: ["Review requested access"] }] });
    if (path === `/api/v1/permissions/${record.id}` && request.method() === "GET") return route.fulfill({ json: record });
    if (path === `/api/v1/permissions/${record.id}/decisions` && request.method() === "POST") {
      submissions.push({ body: request.postDataJSON(), key: request.headers()["idempotency-key"] ?? null });
      record.status = "response_queued"; record.record.status = "response_queued"; record.record.revision = 2;
      return route.fulfill({ status: 202, json: { id: "permission-op-fs-1", state: "accepted" } });
    }
    return route.fulfill({ status: 404, json: { error: { message: `Unexpected smoke-test request: ${path}` } } });
  });

  await page.goto("/");
  await page.getByRole("button", { name: "Review evidence and act" }).click();
  await expect(page.getByText("/tmp/fixtures/input", { exact: true })).toBeVisible();
  await expect(page.getByText("/tmp/fixtures/output", { exact: true })).toBeVisible();
  const approve = page.getByRole("button", { name: "Approve exact paths for this turn" });
  await expect(approve).toBeDisabled();
  await page.getByRole("checkbox", { name: "I approve the listed filesystem permissions for this turn" }).check();
  await expect(approve).toBeEnabled();
  await approve.click();
  await expect(page.getByText("Waiting for Codex to confirm the permission decision", { exact: true })).toBeVisible();
  expect(submissions).toHaveLength(1);
  expect(submissions[0].body).toMatchObject({ decision: "grant", expected_revision: 1 });
  expect(submissions[0].key).toBeTruthy();
});

test("failed queue and run retries plus daemon controls are actionable on the Action Desk", async ({ page }) => {
  const records = new Map<string, any>([
    ["queue-failed", { id: "queue-failed", type: "queue", project: "smoke-project", status: "failed",
      summary: "Queue item failed", updated_at: "queue-rev-1", record: {}, evidence: { error: "worker exited" } }],
    ["run-failed", { id: "run-failed", type: "run", project: "smoke-project", status: "failed",
      summary: "Run failed", updated_at: "run-rev-1", record: {}, evidence: { error: "provider exited" } }],
    ["daemon-failed", { id: "daemon-failed", type: "daemon", project: "smoke-project", status: "failed",
      summary: "Queue daemon failed", updated_at: "daemon-rev-1", record: {}, evidence: { error: "worker stopped" } }],
    ["daemon-running", { id: "daemon-running", type: "daemon", project: "smoke-project", status: "running",
      summary: "Queue daemon running", updated_at: "daemon-rev-2", record: {}, evidence: { pid: 123 } }],
    ["run-active", { id: "run-active", type: "run", project: "smoke-project", status: "running",
      summary: "Active run", updated_at: "run-active-rev-1", record: { process_id: 321 }, evidence: {} }],
    ["queue-active", { id: "queue-active", type: "queue", project: "smoke-project", status: "queued",
      summary: "Queued item", updated_at: "queue-active-rev-1", record: {}, evidence: {} }],
    ["session-active", { id: "session-active", type: "session", project: "smoke-project", status: "running",
      summary: "Active session", updated_at: "session-active-rev-1", record: {}, evidence: {} }],
  ]);
  const cards = [...records.values()].map((record) => ({
    run_id: record.id, subject_type: record.type, classification: "stopped", summary: record.summary,
    evidence: [record.status], recommended_actions: ["Review current evidence and available actions."],
  }));
  const submitted: Array<{ path: string; body: any; key: string | null }> = [];
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    if (path === "/api/v1/events") return route.fulfill({ status: 204 });
    if (path === "/api/v1/overview") return route.fulfill({ json: { metrics: {}, active: [] } });
    if (path === "/api/v1/projects") return route.fulfill({ json: [{
      name: "smoke-project", validate_commands: [], priorities: [], exclusions: [], protected_paths: [],
      workspace_mode: "worktree", sandbox: "read-only", approval_policy: "on-request",
    }] });
    if (path === "/api/v1/settings") return route.fulfill({ json: {
      revision: "smoke-revision", global: { codex_bin: "codex", claude_bin: "claude", cursor_agent_bin: "cursor-agent",
        codex_model: "gpt-test", codex_reasoning_effort: "high", opencode_enabled: false, opencode_model: null,
        agent_runtime_timeout_seconds: 2700, agent_idle_timeout_seconds: 600, max_concurrent_runs: 3 },
      profiles: {}, projects: [{ name: "smoke-project", validate_commands: [], priorities: [], exclusions: [],
        protected_paths: [], workspace_mode: "worktree", sandbox: "read-only", approval_policy: "on-request" }],
    } });
    if (path === "/api/v1/providers") return route.fulfill({ json: [] });
    if (path === "/api/v1/execution-plans/deferred") return route.fulfill({ json: [] });
    if (path === "/api/v1/inbox") return route.fulfill({ json: cards });
    const recordMatch = path.match(/^\/api\/v1\/records\/([^/]+)$/);
    if (recordMatch && records.has(recordMatch[1])) return route.fulfill({ json: records.get(recordMatch[1]) });
    if (request.method() === "POST" && (
      path === "/api/v1/queue/queue-failed/requeues"
      || path === "/api/v1/executions/run-failed/retries"
      || path === "/api/v1/daemons/start"
      || path === "/api/v1/daemons/daemon-running/restarts"
      || path === "/api/v1/daemons/daemon-running/stops"
      || path === "/api/v1/executions/run-active/controls"
      || path === "/api/v1/executions/run-active/cancellations"
      || path === "/api/v1/executions/queue-active/cancellations"
      || path === "/api/v1/executions/session-active/cancellations"
      || path === "/api/v1/sessions/session-active/steers"
    )) {
      const body = request.postDataJSON() ?? {};
      submitted.push({ path, body, key: request.headers()["idempotency-key"] ?? null });
      if (path === "/api/v1/daemons/start") records.get("daemon-failed").status = "running";
      if (path === "/api/v1/daemons/daemon-running/restarts") records.get("daemon-running").status = "running";
      if (path === "/api/v1/daemons/daemon-running/stops") records.get("daemon-running").status = "stopping";
      const activeRun = records.get("run-active");
      if (path === "/api/v1/executions/run-active/controls") {
        activeRun.status = body.action === "pause" ? "paused" : "running";
        activeRun.updated_at += `-${body.action}`;
      }
      if (path === "/api/v1/executions/run-active/cancellations") activeRun.status = "stopping";
      if (path === "/api/v1/executions/queue-active/cancellations") records.get("queue-active").status = "cancelled";
      if (path === "/api/v1/executions/session-active/cancellations") records.get("session-active").status = "stopping";
      return route.fulfill({ json: {
        state: "succeeded", id: `op-${submitted.length}`,
        result: path === "/api/v1/sessions/session-active/steers" ? "queued-steer" : "queued-retry",
      } });
    }
    return route.fulfill({ status: 404, json: { error: { message: `Unexpected smoke-test request: ${path}` } } });
  });

  await page.goto("/");
  await expect(page.getByText("7 open items")).toBeVisible();
  const actions = [
    { id: "queue-failed", button: "Retry queue item", status: /Retry queued/ },
    { id: "run-failed", button: "Retry as new run", status: /New run queued/ },
    { id: "daemon-failed", button: "Start daemon", status: /Daemon started/ },
    { id: "daemon-running", button: "Restart daemon", status: /Daemon restarted/ },
    { id: "daemon-running", button: "Stop daemon", status: /Daemon stop requested/ },
  ];
  for (const action of actions) {
    await page.locator(".action-card").filter({ hasText: records.get(action.id).summary })
      .getByRole("button", { name: /Review evidence and act/ }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByRole("button", { name: action.button })).toBeVisible();
    await dialog.getByRole("button", { name: action.button }).click();
    await expect(dialog.getByRole("status")).toContainText(action.status);
    await expect(page).toHaveURL(/\/$/);
    await dialog.getByRole("button", { name: "Close action details" }).click();
  }

  expect(submitted.map(({ path }) => path)).toEqual([
    "/api/v1/queue/queue-failed/requeues",
    "/api/v1/executions/run-failed/retries",
    "/api/v1/daemons/start",
    "/api/v1/daemons/daemon-running/restarts",
    "/api/v1/daemons/daemon-running/stops",
  ]);
  expect(submitted[0].body).toEqual({ expected_updated_at: "queue-rev-1" });
  expect(submitted[1].body).toEqual({ expected_updated_at: "run-rev-1" });
  expect(submitted[3].body).toEqual({ expected_updated_at: "daemon-rev-2" });

  const openAction = async (id: string) => {
    await page.locator(".action-card").filter({ hasText: records.get(id).summary })
      .getByRole("button", { name: /Review evidence and act/ }).click();
    return page.getByRole("dialog");
  };
  let dialog = await openAction("run-active");
  await dialog.getByRole("button", { name: "Pause provider" }).click();
  await expect(dialog.getByRole("status")).toContainText("Pause requested");
  await expect(dialog.getByRole("button", { name: "Resume provider" })).toBeVisible();
  await dialog.getByRole("button", { name: "Resume provider" }).click();
  await expect(dialog.getByRole("status")).toContainText("Resume requested");
  await dialog.getByRole("button", { name: "Cancel work" }).click();
  await expect(dialog.getByRole("status")).toContainText("Cancellation requested");
  await dialog.getByRole("button", { name: "Close action details" }).click();

  records.get("run-active").status = "reviewing";
  await page.reload();
  dialog = await openAction("run-active");
  await expect(dialog.getByRole("button", { name: "Cancel work" })).toBeVisible();
  await dialog.getByRole("button", { name: "Cancel work" }).click();
  await expect(dialog.getByRole("status")).toContainText("Cancellation requested");
  await dialog.getByRole("button", { name: "Close action details" }).click();

  dialog = await openAction("queue-active");
  await dialog.getByRole("button", { name: "Cancel work" }).click();
  await expect(dialog.getByRole("status")).toContainText("Cancellation requested");
  await dialog.getByRole("button", { name: "Close action details" }).click();

  dialog = await openAction("session-active");
  await dialog.getByLabel("Direction").fill("Continue after reviewing the retained evidence.");
  await dialog.getByRole("button", { name: "Queue steering" }).click();
  await expect(dialog.getByRole("status")).toContainText("Steering queued");
  await dialog.getByRole("button", { name: "Cancel work" }).click();
  await expect(dialog.getByRole("status")).toContainText("Cancellation requested");
  await expect(page).toHaveURL(/\/$/);

  expect(submitted.slice(5).map(({ path }) => path)).toEqual([
    "/api/v1/executions/run-active/controls",
    "/api/v1/executions/run-active/controls",
    "/api/v1/executions/run-active/cancellations",
    "/api/v1/executions/run-active/cancellations",
    "/api/v1/executions/queue-active/cancellations",
    "/api/v1/sessions/session-active/steers",
    "/api/v1/executions/session-active/cancellations",
  ]);
  expect(submitted[5].body).toEqual({ action: "pause" });
  expect(submitted[6].body).toEqual({ action: "resume" });
  expect(submitted[10].body).toEqual({
    message: "Continue after reviewing the retained evidence.", expected_updated_at: "session-active-rev-1",
  });
  expect(submitted.every(({ key }) => Boolean(key))).toBe(true);
});

test("plan clarification and editing stay on the Action Desk and gate approval", async ({ page }) => {
  const fixture = planFixture();
  await mockDeskApi(page, fixture);
  await page.goto("/");

  const reviewButton = page.getByRole("button", { name: /Review evidence and act/ });
  await expect(reviewButton).toBeVisible();
  await reviewButton.click();

  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  await expect(page).toHaveURL(/\/$/);
  await expect(dialog.getByRole("button", { name: /Approve plan/ })).toHaveCount(0);
  await dialog.getByLabel("Clarification").fill("Completion means unresolved requirements are answered.");
  await dialog.getByRole("button", { name: /Save answer as a new plan version/ }).click();

  await expect(dialog.getByRole("status")).toHaveText("Clarification saved as plan v2");
  await expect(dialog.getByRole("button", { name: /Approve plan v2 and queue/ })).toBeVisible();
  expect(fixture.submissions[0]).toEqual({
    kind: "answer",
    body: { version: 1, answer: "Completion means unresolved requirements are answered." },
  });

  await dialog.getByText("Edit plan scope").click();
  await dialog.getByLabel("Requested outcome").fill("Keep each decision completable in the Action Desk drawer.");
  await dialog.getByRole("button", { name: "Save new plan version" }).click();
  await expect(dialog.getByRole("status")).toHaveText("Saved as plan v3; review and approve it to queue work.");
  await expect(dialog.getByRole("button", { name: /Approve plan v3 and queue/ })).toBeVisible();
  expect(fixture.submissions[1].kind).toBe("edit");
  expect(fixture.submissions[1].body).toMatchObject({ version: 2, objective: "Keep each decision completable in the Action Desk drawer." });
  expect(fixture.submissions).toHaveLength(2);
  await expect(page).toHaveURL(/\/$/);

  await dialog.getByRole("button", { name: "Close action details" }).click();
  await expect(dialog).not.toBeVisible();
  await expect(reviewButton).toBeFocused();
});

test("plan deferral preserves a draft and restores review from the Action Desk without queueing", async ({ page }) => {
  const fixture = planFixture([]);
  await mockDeskApi(page, fixture);
  await page.goto("/");

  await page.getByRole("button", { name: /Review evidence and act/ }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("button", { name: "Defer plan" }).click();

  await expect(dialog.getByRole("status")).toContainText("draft is preserved and nothing was queued");
  await expect(page).toHaveURL(/\/$/);
  await dialog.getByRole("button", { name: "Close action details" }).click();
  await expect(page.getByText("No open decisions or follow-ups.")).toBeVisible();
  const deferred = page.locator(".deferred-plans");
  await expect(deferred.getByRole("heading", { name: /Deferred plans 1/ })).toBeVisible();
  await deferred.getByRole("button", { name: "Restore to draft review" }).click();

  await expect(deferred.getByRole("status")).toContainText("Restored smoke-task v1 to draft review");
  await expect(deferred.getByText("No deferred plans.")).toBeVisible();
  await expect(page.locator(".action-card")).toHaveCount(1);
  await page.locator(".action-card").getByRole("button", { name: /Review evidence and act/ }).click();
  await expect(page.getByRole("dialog").getByRole("button", { name: /Approve plan v1 and queue/ })).toBeVisible();
  expect(fixture.submissions.map(({ kind }) => kind)).toEqual(["defer", "restore"]);
  expect(fixture.submissions[0].body).toEqual({ version: 1 });
  expect(fixture.submissions[0].key).toBe("desk:defer-plan:smoke-task:1");
  expect(fixture.submissions[1].body).toEqual({ version: 1 });
  expect(fixture.submissions[1].key).toBe("desk:restore-plan:smoke-task:1");
});

test("candidate sources keep diff, exact preview, and adopt or reject decisions on the Action Desk", async ({ page }) => {
  const records = new Map<string, any>([
    ["pipeline-candidate", { id: "pipeline-candidate", type: "pipeline", project: "smoke-project", status: "awaiting_decision",
      summary: "Pipeline candidate", updated_at: "pipeline-rev", record: {}, evidence: {} }],
    ["assessment-candidate", { id: "assessment-candidate", type: "assessment", project: "smoke-project", status: "awaiting_decision",
      summary: "Assessment candidate", updated_at: "assessment-rev", record: {}, evidence: {} }],
    ["duel-candidate", { id: "duel-candidate", type: "duel", project: "smoke-project", status: "awaiting_decision",
      summary: "Duel candidate", updated_at: "duel-rev", record: {}, evidence: { participants: [{ provider: "provider-alpha" }] } }],
    ["review-candidate", { id: "review-candidate", type: "cross_review", project: "smoke-project", status: "awaiting_decision",
      summary: "Cross-review candidate", updated_at: "review-rev", record: {},
      evidence: { participants: [{ provider: "provider-alpha" }], cross_review: { duel_id: "linked-duel" } } }],
    ["retained-run", { id: "retained-run", type: "run", project: "smoke-project", status: "succeeded",
      summary: "Retained isolated candidate", updated_at: "run-rev", record: {}, evidence: { candidate_available: true } }],
  ]);
  const cards = [...records.values()].map((record) => ({
    run_id: record.id, subject_type: record.type, classification: "decision_required", summary: record.summary,
    evidence: [record.status], recommended_actions: ["Inspect candidate and choose a decision"],
  }));
  const decisions: any[] = [];
  const diffRequests: string[] = [];
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    if (path === "/api/v1/events") return route.fulfill({ status: 204 });
    if (path === "/api/v1/overview") return route.fulfill({ json: { metrics: {}, active: [] } });
    if (path === "/api/v1/projects") return route.fulfill({ json: [{
      name: "smoke-project", validate_commands: [], priorities: [], exclusions: [], protected_paths: [],
      workspace_mode: "worktree", sandbox: "read-only", approval_policy: "on-request",
    }] });
    if (path === "/api/v1/settings") return route.fulfill({ json: {
      revision: "smoke-revision", global: { codex_bin: "codex", claude_bin: "claude", cursor_agent_bin: "cursor-agent",
        codex_model: "gpt-test", codex_reasoning_effort: "high", opencode_enabled: false, opencode_model: null,
        agent_runtime_timeout_seconds: 2700, agent_idle_timeout_seconds: 600, max_concurrent_runs: 3 },
      profiles: {}, projects: [],
    } });
    if (path === "/api/v1/providers") return route.fulfill({ json: [] });
    if (path === "/api/v1/execution-plans/deferred") return route.fulfill({ json: [] });
    if (path === "/api/v1/inbox") return route.fulfill({ json: cards });
    const recordPath = path.match(/^\/api\/v1\/records\/([^/]+)$/);
    if (recordPath && records.has(recordPath[1])) return route.fulfill({ json: records.get(recordPath[1]) });
    if (path.endsWith("/diff")) {
      diffRequests.push(`${path}${url.search}`);
      return route.fulfill({ json: { patch: "diff --git a/src/example.py b/src/example.py\n+verified change" } });
    }
    if (path.endsWith("/adoption-preview")) return route.fulfill({ json: {
      candidate_digest: `candidate-${url.pathname.split("/")[4]}`, destination_commit: "base-commit",
      changed_files: ["src/example.py"], protected_files: [], dirty_destination: false,
    } });
    if (path === "/api/v1/decisions" && request.method() === "POST") {
      decisions.push({ body: request.postDataJSON(), key: request.headers()["idempotency-key"] });
      return route.fulfill({ json: { state: "succeeded", id: `decision-${decisions.length}` } });
    }
    return route.fulfill({ status: 404, json: { error: { message: `Unexpected candidate request: ${path}` } } });
  });

  await page.goto("/");
  const cases = [
    { id: "pipeline-candidate", summary: "Pipeline candidate", action: "adopt" },
    { id: "assessment-candidate", summary: "Assessment candidate", action: "adopt" },
    { id: "duel-candidate", summary: "Duel candidate", action: "adopt" },
    { id: "review-candidate", summary: "Cross-review candidate", action: "reject" },
    { id: "retained-run", summary: "Retained isolated candidate", action: "adopt" },
  ];
  for (const item of cases) {
    await page.locator(".action-card").filter({ hasText: item.summary })
      .getByRole("button", { name: /Review evidence and act/ }).click();
    const dialog = page.getByRole("dialog");
    if (item.id === "duel-candidate" || item.id === "review-candidate") {
      await expect(dialog.getByLabel("Candidate provider")).toHaveValue("provider-alpha");
    }
    await dialog.getByRole("button", { name: "Inspect diff" }).click();
    await expect(dialog.getByLabel("Candidate diff")).toContainText("verified change");
    await dialog.getByRole("button", { name: "Preview adoption" }).click();
    await expect(dialog.getByText("base-commit")).toBeVisible();
    await dialog.getByRole("button", { name: item.action === "adopt" ? "Adopt changes" : "Reject candidate" }).click();
    await expect(dialog.getByRole("status")).toContainText(item.action === "adopt" ? "Adopted" : "Rejected");
    await expect(page).toHaveURL(/\/$/);
    await dialog.getByRole("button", { name: "Close action details" }).click();
  }

  expect(decisions.map(({ body }) => body.action)).toEqual(["adopt", "adopt", "adopt", "reject", "adopt"]);
  expect(decisions[3].body).toMatchObject({ id: "linked-duel", cross_review_id: "review-candidate", provider: "provider-alpha" });
  expect(decisions.every(({ key }) => Boolean(key))).toBe(true);
  expect(diffRequests).toContain("/api/v1/records/linked-duel/diff?provider=provider-alpha");
});

test("protected and stale candidate decisions remain explicit and revision-bound in the drawer", async ({ page }) => {
  const records = new Map<string, any>([
    ["protected-candidate", { id: "protected-candidate", type: "pipeline", project: "smoke-project", status: "awaiting_decision",
      summary: "Protected candidate", updated_at: "protected-rev", record: {}, evidence: {} }],
    ["dirty-candidate", { id: "dirty-candidate", type: "assessment", project: "smoke-project", status: "awaiting_decision",
      summary: "Dirty destination candidate", updated_at: "dirty-rev", record: {}, evidence: {} }],
  ]);
  const cards = [...records.values()].map((record) => ({
    run_id: record.id, subject_type: record.type, classification: "decision_required", summary: record.summary,
    evidence: [record.status], recommended_actions: ["Inspect candidate and choose a decision"],
  }));
  const submitted: any[] = [];
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    if (path === "/api/v1/events") return route.fulfill({ status: 204 });
    if (path === "/api/v1/overview") return route.fulfill({ json: { metrics: {}, active: [] } });
    if (path === "/api/v1/projects") return route.fulfill({ json: [{ name: "smoke-project", validate_commands: [],
      priorities: [], exclusions: [], protected_paths: ["secrets/"], workspace_mode: "worktree",
      sandbox: "read-only", approval_policy: "on-request" }] });
    if (path === "/api/v1/settings") return route.fulfill({ json: { revision: "smoke-revision", global: {}, profiles: {}, projects: [] } });
    if (path === "/api/v1/providers") return route.fulfill({ json: [] });
    if (path === "/api/v1/execution-plans/deferred") return route.fulfill({ json: [] });
    if (path === "/api/v1/inbox") return route.fulfill({ json: cards });
    const recordPath = path.match(/^\/api\/v1\/records\/([^/]+)$/);
    if (recordPath && records.has(recordPath[1])) return route.fulfill({ json: records.get(recordPath[1]) });
    if (path.endsWith("/diff")) return route.fulfill({ json: { patch: "+protected edit" } });
    if (path.endsWith("/adoption-preview")) {
      const protectedCandidate = path.includes("protected-candidate");
      return route.fulfill({ json: {
        candidate_digest: protectedCandidate ? "digest-protected" : "digest-dirty",
        destination_commit: "base-commit", changed_files: [protectedCandidate ? "secrets/token.txt" : "src/file.py"],
        protected_files: protectedCandidate ? ["secrets/token.txt"] : [], dirty_destination: !protectedCandidate,
      } });
    }
    if (path === "/api/v1/decisions" && request.method() === "POST") {
      const body = request.postDataJSON();
      submitted.push(body);
      if (body.id === "protected-candidate") return route.fulfill({ status: 409, json: { error: { message: "Candidate changed since preview." } } });
      return route.fulfill({ json: { state: "succeeded", id: "reject-dirty" } });
    }
    return route.fulfill({ status: 404, json: { error: { message: `Unexpected protected candidate request: ${path}` } } });
  });

  await page.goto("/");
  await page.locator(".action-card").filter({ hasText: "Protected candidate" })
    .getByRole("button", { name: /Review evidence and act/ }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("button", { name: "Preview adoption" }).click();
  const adopt = dialog.getByRole("button", { name: "Adopt changes" });
  await expect(adopt).toBeDisabled();
  await dialog.getByLabel("I approve changing these protected paths").check();
  await expect(adopt).toBeEnabled();
  await adopt.click();
  await expect(dialog.getByRole("alert")).toContainText("Candidate changed since preview");
  expect(submitted).toHaveLength(1);

  await dialog.getByRole("button", { name: "Close action details" }).click();
  await page.locator(".action-card").filter({ hasText: "Dirty destination candidate" })
    .getByRole("button", { name: /Review evidence and act/ }).click();
  const dirtyDialog = page.getByRole("dialog");
  await dirtyDialog.getByRole("button", { name: "Preview adoption" }).click();
  await expect(dirtyDialog.getByRole("button", { name: "Adopt changes" })).toBeDisabled();
  await expect(dirtyDialog.getByText("Destination has local changes. Resolve them before adoption.")).toBeVisible();
  await dirtyDialog.getByRole("button", { name: "Reject candidate" }).click();
  await expect(dirtyDialog.getByRole("status")).toContainText("Rejected");
  expect(submitted).toHaveLength(2);
  expect(submitted.map(({ action }) => action)).toEqual(["adopt", "reject"]);
});

test("a stale plan edit remains an in-place Action Desk decision", async ({ page }) => {
  const fixture = planFixture([]);
  await mockDeskApi(page, fixture, 409);
  await page.goto("/");
  await page.getByRole("button", { name: /Review evidence and act/ }).click();

  const dialog = page.getByRole("dialog");
  await dialog.getByText("Edit plan scope").click();
  await dialog.getByLabel("Requested outcome").fill("A stale edit should show a clear conflict.");
  await dialog.getByRole("button", { name: "Save new plan version" }).click();

  await expect(dialog.getByRole("alert")).toContainText("Current plan version v2");
  await expect(page).toHaveURL(/\/$/);
});

test("SSE refresh updates drawer evidence without erasing an unsent plan edit", async ({ page }) => {
  const fixture = planFixture([]);
  await mockDeskApi(page, fixture, 409);
  await page.addInitScript(() => {
    const listeners = new Map<string, Array<() => void>>();
    const source = {
      addEventListener(type: string, listener: () => void) {
        listeners.set(type, [...(listeners.get(type) ?? []), listener]);
      },
      close() {},
      emit(type: string) { for (const listener of listeners.get(type) ?? []) listener(); },
    };
    Object.defineProperty(window, "EventSource", { configurable: true, value: class { close() {} addEventListener = source.addEventListener; } });
    (window as any).__whirlwindEventSource = source;
  });
  await page.goto("/");
  await page.getByRole("button", { name: /Review evidence and act/ }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByText("Edit plan scope").click();
  const objective = dialog.getByLabel("Requested outcome");
  await objective.fill("My unsent local edit");

  fixture.plan.summary = "Plan changed elsewhere";
  fixture.plan.updated_at = "2026-10-11T00:05:00Z";
  fixture.plan.record.version = 2;
  fixture.plan.record.content.objective = "Current objective from another reviewer";
  fixture.plan.evidence = { plan: { version: 2, content: fixture.plan.record.content } };
  await page.evaluate(() => (window as any).__whirlwindEventSource.emit("update"));

  await expect(dialog.locator("pre").first()).toContainText("Current objective from another reviewer");
  await expect(objective).toHaveValue("My unsent local edit");
  await dialog.getByRole("button", { name: "Save new plan version" }).click();

  await expect(dialog.getByRole("alert")).toContainText("Current plan version v2");
  await expect(objective).toHaveValue("My unsent local edit");
  await expect(dialog.locator("pre").first()).toContainText("Current objective from another reviewer");
  expect(fixture.submissions).toHaveLength(1);
  expect(fixture.submissions[0].body.version).toBe(1);
  await expect(page).toHaveURL(/\/$/);
});

test("SSE refresh preserves an unsent run reply and reports the latest revision on conflict", async ({ page }) => {
  const run: any = {
    id: "waiting-run", type: "run", project: "smoke-project", status: "needs_human",
    summary: "Run needs a clarification", updated_at: "rev-1", record: {},
    evidence: { question: "Old question" },
  };
  let replyBody: any = null;
  await page.addInitScript(() => {
    const listeners = new Map<string, Array<() => void>>();
    const source = {
      addEventListener(type: string, listener: () => void) {
        listeners.set(type, [...(listeners.get(type) ?? []), listener]);
      },
      close() {},
      emit(type: string) { for (const listener of listeners.get(type) ?? []) listener(); },
    };
    Object.defineProperty(window, "EventSource", { configurable: true, value: class { close() {} addEventListener = source.addEventListener; } });
    (window as any).__whirlwindEventSource = source;
  });
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path === "/api/v1/events") return route.fulfill({ status: 204 });
    if (path === "/api/v1/overview") return route.fulfill({ json: { metrics: {}, active: [] } });
    if (path === "/api/v1/projects") return route.fulfill({ json: [{
      name: "smoke-project", validate_commands: [], priorities: [], exclusions: [], protected_paths: [],
      workspace_mode: "worktree", sandbox: "read-only", approval_policy: "on-request",
    }] });
    if (path === "/api/v1/settings") return route.fulfill({ json: {
      revision: "smoke-revision", global: { codex_bin: "codex", claude_bin: "claude", cursor_agent_bin: "cursor-agent",
        codex_model: "gpt-test", codex_reasoning_effort: "high", opencode_enabled: false, opencode_model: null,
        agent_runtime_timeout_seconds: 2700, agent_idle_timeout_seconds: 600, max_concurrent_runs: 3 },
      profiles: {}, projects: [{ name: "smoke-project", validate_commands: [], priorities: [], exclusions: [],
        protected_paths: [], workspace_mode: "worktree", sandbox: "read-only", approval_policy: "on-request" }],
    } });
    if (path === "/api/v1/providers") return route.fulfill({ json: [] });
    if (path === "/api/v1/execution-plans/deferred") return route.fulfill({ json: [] });
    if (path === "/api/v1/inbox") return route.fulfill({ json: [{
      run_id: run.id, subject_type: "run", classification: "decision_required", summary: run.summary,
      evidence: [run.status], recommended_actions: ["Answer the run"],
    }] });
    if (path === `/api/v1/records/${run.id}`) return route.fulfill({ json: run });
    if (path === `/api/v1/executions/${run.id}/replies` && request.method() === "POST") {
      replyBody = request.postDataJSON();
      run.updated_at = "rev-3";
      run.summary = "Run changed again before the reply was accepted";
      run.evidence = { question: "Latest question" };
      return route.fulfill({ status: 409, json: { error: { message: "The run has changed." } } });
    }
    return route.fulfill({ status: 404, json: { error: { message: `Unexpected request: ${path}` } } });
  });

  await page.goto("/");
  await page.getByRole("button", { name: /Review evidence and act/ }).click();
  const dialog = page.getByRole("dialog");
  const reply = dialog.getByLabel("Clarification");
  await reply.fill("My reply based on the original question");

  run.updated_at = "rev-2";
  run.summary = "Run changed while the reply was open";
  run.evidence = { question: "Updated question" };
  await page.evaluate(() => (window as any).__whirlwindEventSource.emit("update"));

  await expect(dialog.locator("pre").first()).toContainText("Updated question");
  await expect(reply).toHaveValue("My reply based on the original question");
  await dialog.getByRole("button", { name: "Send answer and continue" }).click();

  await expect(dialog.getByRole("alert")).toContainText("Current revision rev-3");
  await expect(dialog.getByRole("alert")).toContainText("your reply is preserved");
  await expect(dialog.locator("pre").first()).toContainText("Latest question");
  await expect(reply).toHaveValue("My reply based on the original question");
  expect(replyBody).toEqual({ reply: "My reply based on the original question", expected_updated_at: "rev-2" });
  await expect(page).toHaveURL(/\/$/);
});

test("run checkpoint history is reviewable inside the Action Desk drawer", async ({ page }) => {
  const run = {
    id: "checkpoint-run", type: "run", project: "smoke-project", status: "needs_human",
    summary: "Run stopped for review", updated_at: "rev-1",
    record: { status: "needs_human", execution_plan_version: 3 },
    evidence: { reason: "Review the retained work." },
    checkpoints: [{
      id: "checkpoint-1", run_id: "checkpoint-run", sequence: 1,
      approved_plan_version: 3, policy_digest: "policy-v3", created_at: "2026-10-11T00:00:00Z",
      content: {
        phase: "verification_passed", workspace: "/tmp/whirlwind-worktree", workspace_head: "base-commit",
        candidate_digest: "candidate-digest", workspace_dirty: true,
        verified_steps: ["make test passed for candidate-digest"],
        provider_lineage: [{ provider: "codex", role: "worker", status: "succeeded", session_id: "codex-session" }],
        remaining_budget_seconds: 95, failure_fingerprints: [],
        artifact_references: ["validation-1.json"],
      },
    }, {
      id: "checkpoint-2", run_id: "checkpoint-run", sequence: 2,
      approved_plan_version: 3, policy_digest: "policy-v3", created_at: "2026-10-11T00:01:00Z",
      content: {
        phase: "interrupted", workspace: "/tmp/whirlwind-worktree", workspace_head: "new-head",
        candidate_digest: null, workspace_dirty: null,
        verified_steps: ["make test passed for candidate-digest"],
        provider_lineage: [{ provider: "codex", role: "worker", status: "failed", session_id: "codex-session" }],
        remaining_budget_seconds: null, failure_fingerprints: ["failed-check"],
        artifact_references: ["validation-1.json"],
      },
    }],
  };
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/api/v1/events") return route.fulfill({ status: 204 });
    if (path === "/api/v1/overview") return route.fulfill({ json: { metrics: {}, active: [] } });
    if (path === "/api/v1/projects") return route.fulfill({ json: [{
      name: "smoke-project", validate_commands: [], priorities: [], exclusions: [], protected_paths: [],
      workspace_mode: "worktree", sandbox: "read-only", approval_policy: "on-request",
    }] });
    if (path === "/api/v1/settings") return route.fulfill({ json: {
      revision: "smoke-revision", global: {}, profiles: {}, projects: [],
    } });
    if (path === "/api/v1/providers") return route.fulfill({ json: [] });
    if (path === "/api/v1/execution-plans/deferred") return route.fulfill({ json: [] });
    if (path === "/api/v1/inbox") return route.fulfill({ json: [{
      run_id: run.id, subject_type: "run", classification: "decision_required",
      summary: run.summary, evidence: ["needs_human"], recommended_actions: ["Review checkpoint"],
    }] });
    if (path === `/api/v1/records/${run.id}`) return route.fulfill({ json: run });
    return route.fulfill({ status: 404, json: { error: { message: `Unexpected request: ${path}` } } });
  });

  await page.goto("/");
  await page.getByRole("button", { name: /Review evidence and act/ }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("heading", { name: "Execution checkpoints" })).toBeVisible();
  await expect(dialog.getByText("interrupted", { exact: true })).toBeVisible();
  await expect(dialog.getByText("Unavailable", { exact: true }).first()).toBeVisible();
  await expect(dialog.getByRole("heading", { name: "Answer this run" })).toBeVisible();
  await expect(dialog.getByLabel("Clarification")).toBeVisible();
  await expect(dialog.getByText("verification passed", { exact: false })).toBeVisible();
  await expect(dialog.getByText("make test passed for candidate-digest").first()).toBeVisible();
  await expect(dialog.getByText("codex · worker · succeeded")).toBeVisible();
  await expect(dialog.getByText("95 seconds remaining")).toBeVisible();
  await expect(page).toHaveURL(/\/$/);
});

test("repeated validation and Reviewer FIX failures are reviewable on the Action Desk", async ({ page }) => {
  const scenarios = [
    {
      run: {
        id: "validation-stop", type: "run", project: "smoke-project", status: "needs_human",
        summary: "Repeated validation failure; Whirlwind stopped for your direction.", updated_at: "validation-rev-1",
        record: { failure_fingerprint: "validation-abc123", repeated_failures: 2 },
        evidence: {
          run: { status: "needs_human", failure_fingerprint: "validation-abc123", repeated_failures: 2 },
          events: [{ phase: "failure", message: "tests/check.sh failed twice with the same output." }],
          artifacts: ["validation-2.json"],
        },
      },
      fingerprint: "validation-abc123",
      detail: "tests/check.sh failed twice with the same output.",
      artifact: "validation-2.json",
      reply: "Fix the assertion in tests/check.sh, then rerun the failed check.",
    },
    {
      run: {
        id: "review-stop", type: "run", project: "smoke-project", status: "needs_human",
        summary: "Reviewer repeated the same FIX findings; inspect retained work before continuing.", updated_at: "review-rev-1",
        record: { failure_fingerprint: "review-def456", repeated_failures: 2 },
        evidence: {
          run: { status: "needs_human", failure_fingerprint: "review-def456", repeated_failures: 2 },
          events: [{ phase: "reviewing", message: "Reviewer FIX: missing boundary validation." }],
          artifacts: ["review-2.md"],
        },
      },
      fingerprint: "review-def456",
      detail: "Reviewer FIX: missing boundary validation.",
      artifact: "review-2.md",
      reply: "Add the missing boundary validation and ask the reviewer to check again.",
    },
  ];
  const runs = new Map(scenarios.map(({ run }) => [run.id, run]));
  const replyBodies: Array<{ run_id: string; body: any }> = [];
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path === "/api/v1/events") return route.fulfill({ status: 204 });
    if (path === "/api/v1/overview") return route.fulfill({ json: { metrics: {}, active: [] } });
    if (path === "/api/v1/projects") return route.fulfill({ json: [{
      name: "smoke-project", validate_commands: [], priorities: [], exclusions: [], protected_paths: [],
      workspace_mode: "worktree", sandbox: "read-only", approval_policy: "on-request",
    }] });
    if (path === "/api/v1/settings") return route.fulfill({ json: {
      revision: "smoke-revision", global: { codex_bin: "codex", claude_bin: "claude", cursor_agent_bin: "cursor-agent",
        codex_model: "gpt-test", codex_reasoning_effort: "high", opencode_enabled: false, opencode_model: null,
        agent_runtime_timeout_seconds: 2700, agent_idle_timeout_seconds: 600, max_concurrent_runs: 3 },
      profiles: {}, projects: [{ name: "smoke-project", validate_commands: [], priorities: [], exclusions: [], protected_paths: [],
        workspace_mode: "worktree", sandbox: "read-only", approval_policy: "on-request" }],
    } });
    if (path === "/api/v1/providers") return route.fulfill({ json: [] });
    if (path === "/api/v1/execution-plans/deferred") return route.fulfill({ json: [] });
    if (path === "/api/v1/inbox") return route.fulfill({ json: [...runs.values()]
      .filter((run) => run.status === "needs_human")
      .map((run) => ({ run_id: run.id, subject_type: "run", classification: "decision_required",
        summary: run.summary, evidence: [run.status], recommended_actions: ["Review the stopped run"] })) });
    const recordId = path.startsWith("/api/v1/records/") ? path.slice("/api/v1/records/".length) : "";
    if (runs.has(recordId)) return route.fulfill({ json: runs.get(recordId) });
    const replyMatch = path.match(/^\/api\/v1\/executions\/([^/]+)\/replies$/);
    if (replyMatch && request.method() === "POST") {
      const run = runs.get(replyMatch[1]);
      if (!run) return route.fulfill({ status: 404, json: { error: { message: "Unknown run" } } });
      replyBodies.push({ run_id: run.id, body: request.postDataJSON() });
      run.status = "queued";
      run.summary = "Repair direction received; continuation queued.";
      run.updated_at = `${run.id}-rev-2`;
      run.evidence.run.status = "queued";
      return route.fulfill({ json: { state: "succeeded" } });
    }
    return route.fulfill({ status: 404, json: { error: { message: `Unexpected request: ${path}` } } });
  });

  await page.goto("/");
  for (const [index, scenario] of scenarios.entries()) {
    await expect(page.getByRole("heading", { level: 3, name: scenario.run.summary })).toBeVisible();
    await page.locator(".action-card").filter({ hasText: scenario.run.summary })
      .getByRole("button", { name: /Review evidence and act/ }).click();
    const dialog = page.getByRole("dialog");
    const evidence = dialog.locator("pre").first();
    await expect(evidence).toContainText(scenario.fingerprint);
    await expect(evidence).toContainText(scenario.detail);
    await expect(evidence).toContainText(scenario.artifact);
    if (index === 0) {
      const abandonedDraft = dialog.getByLabel("Clarification");
      await abandonedDraft.fill("I am still reviewing this failure.");
      await dialog.getByRole("button", { name: "Close action details" }).click();
      await expect(dialog).not.toBeVisible();
      expect(replyBodies).toHaveLength(0);
      await expect(page.locator(".action-card").filter({ hasText: scenario.run.summary })).toBeVisible();
      await page.locator(".action-card").filter({ hasText: scenario.run.summary })
        .getByRole("button", { name: /Review evidence and act/ }).click();
      await expect(page.getByRole("dialog").locator("pre").first()).toContainText(scenario.fingerprint);
    }
    const reply = dialog.getByLabel("Clarification");
    await reply.fill(scenario.reply);
    await dialog.getByRole("button", { name: "Send answer and continue" }).click();

    await expect(dialog.getByRole("status")).toHaveText("Clarification recorded · continuation queued for this run.");
    await expect(reply).not.toBeVisible();
    expect(replyBodies[index]).toEqual({ run_id: scenario.run.id,
      body: { reply: scenario.reply, expected_updated_at: scenario.run.id === "validation-stop" ? "validation-rev-1" : "review-rev-1" } });
    await expect(page).toHaveURL(/\/$/);
    await dialog.getByRole("button", { name: "Close action details" }).click();
  }
});

test("interrupted configuration reconciliation completes from the Action Desk drawer", async ({ page }) => {
  const successful: any = {
    id: "operation-stuck", type: "operation", state: "reconciling", kind: "settings.update",
    target_id: "settings", updated_at: "2026-10-11T12:00:00Z", can_reconcile: true,
  };
  const conflicted: any = {
    id: "operation-conflict", type: "operation", state: "reconciling", kind: "project.update",
    target_id: "smoke-project", updated_at: "2026-10-11T12:01:00Z", can_reconcile: true,
  };
  const operations = new Map([[successful.id, successful], [conflicted.id, conflicted]]);
  let needsAttention = new Set(operations.keys());
  let submitted: { body: any; key: string | null } | null = null;
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    if (path === "/api/v1/events") return route.fulfill({ status: 204 });
    if (path === "/api/v1/overview") return route.fulfill({ json: { metrics: {}, active: [] } });
    if (path === "/api/v1/projects") return route.fulfill({ json: [{
      name: "smoke-project", validate_commands: [], priorities: [], exclusions: [], protected_paths: [],
      workspace_mode: "worktree", sandbox: "read-only", approval_policy: "on-request",
    }] });
    if (path === "/api/v1/settings") return route.fulfill({ json: {
      revision: "smoke-revision", global: { codex_bin: "codex", claude_bin: "claude", cursor_agent_bin: "cursor-agent",
        codex_model: "gpt-test", codex_reasoning_effort: "high", opencode_enabled: false, opencode_model: null,
        agent_runtime_timeout_seconds: 2700, agent_idle_timeout_seconds: 600, max_concurrent_runs: 3 },
      profiles: {}, projects: [],
    } });
    if (path === "/api/v1/providers") return route.fulfill({ json: [] });
    if (path === "/api/v1/execution-plans/deferred") return route.fulfill({ json: [] });
    if (path === "/api/v1/inbox") return route.fulfill({ json: [...needsAttention].map((id) => {
      const operation = operations.get(id)!;
      return {
        run_id: operation.id, subject_type: "operation", classification: "decision_required",
        summary: `Configuration change needs reconciliation · ${operation.target_id}`,
        evidence: [`operation=${operation.kind}`, "state=reconciling", `updated_at=${operation.updated_at}`],
        recommended_actions: ["Retry the recorded change only if it remains intended"],
      };
    }) });
    const operationMatch = path.match(/^\/api\/v1\/operations\/([^/]+)$/);
    if (operationMatch && request.method() === "GET" && operations.has(operationMatch[1])) {
      return route.fulfill({ json: operations.get(operationMatch[1]) });
    }
    const reconcileMatch = path.match(/^\/api\/v1\/operations\/([^/]+)\/reconciliations$/);
    if (reconcileMatch && request.method() === "POST") {
      const operation = operations.get(reconcileMatch[1]);
      if (!operation) return route.fulfill({ status: 404, json: { error: { message: "Unknown operation" } } });
      if (operation.id === conflicted.id) {
        return route.fulfill({ status: 409, json: { error: { message: "Configuration changed after this operation; review the current settings." } } });
      }
      submitted = { body: request.postDataJSON(), key: request.headers()["idempotency-key"] ?? null };
      operation.state = "succeeded";
      operation.can_reconcile = false;
      needsAttention.delete(operation.id);
      return route.fulfill({ json: { id: "op-reconciled", state: "succeeded" } });
    }
    return route.fulfill({ status: 404, json: { error: { message: `Unexpected smoke-test request: ${path}` } } });
  });

  await page.goto("/");
  await expect(page.getByText("2 open items")).toBeVisible();
  const card = page.locator(".action-card").filter({ hasText: "Configuration change needs reconciliation · settings" });
  await card.getByRole("button", { name: /Review evidence and act/ }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("heading", { name: "Reconcile interrupted configuration" })).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Retry safe reconciliation" })).toBeVisible();
  await expect(page).toHaveURL(/\/$/);
  await dialog.getByRole("button", { name: "Retry safe reconciliation" }).click();
  await expect(dialog.getByRole("status")).toContainText("Configuration operation reconciled · op-recon");
  await expect(page.getByText("1 open item")).toBeVisible();
  expect(submitted).toEqual({
    body: { expected_updated_at: "2026-10-11T12:00:00Z" },
    key: "desk:reconcile:operation-stuck:2026-10-11T12:00:00Z",
  });
  await expect(page).toHaveURL(/\/$/);
  await dialog.getByRole("button", { name: "Close action details" }).click();

  await page.locator(".action-card").filter({ hasText: "Configuration change needs reconciliation · smoke-project" })
    .getByRole("button", { name: /Review evidence and act/ }).click();
  const conflictDialog = page.getByRole("dialog");
  await expect(conflictDialog.getByRole("button", { name: "Retry safe reconciliation" })).toBeVisible();
  await conflictDialog.getByRole("button", { name: "Retry safe reconciliation" }).click();
  await expect(conflictDialog.getByRole("alert")).toContainText("Configuration changed after this operation");
  await expect(page.getByText("1 open item")).toBeVisible();
  await expect(page).toHaveURL(/\/$/);
});
