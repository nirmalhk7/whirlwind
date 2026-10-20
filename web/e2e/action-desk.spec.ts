import { expect, test, type Page, type Route } from "@playwright/test";
import { planFixture, mockDeskApi } from './fixtures/desk-api';
test("manual discovery scan is reviewable from the single-screen Action Desk", async ({ page }) => {
    const fixture = planFixture([]);
    await mockDeskApi(page, fixture, 200, []);
    await page.goto("/");
    await page.getByRole("button", { name: "Scan project now" }).click();
    await expect(page.getByRole("status").filter({ hasText: "Discovery scan completed" })).toBeVisible();
    await expect(page.getByText("src/fragile.py:4 · Follow-up marker")).toBeVisible();
    await expect(page.getByText("src/pending.py · Working-tree change")).toBeVisible();
    await expect(page).toHaveURL(/\/$/);
    expect(fixture.submissions).toContainEqual({ kind: "discovery.scan", body: { trigger: "manual" } });
    await page.reload();
    await expect(page.getByText("src/fragile.py:4 · Follow-up marker")).toBeVisible();
    await expect(page.getByText("src/pending.py · Working-tree change")).toBeVisible();
});
test("discovery proposals can be synthesized, revised, and approved into Planning from the Action Desk", async ({ page }) => {
    const fixture = planFixture([]);
    await mockDeskApi(page, fixture, 200, []);
    await page.goto("/");
    await page.getByRole("button", { name: "Synthesize from latest scan" }).click();
    const proposal = page.getByRole("region", { name: "Discovery proposals" }).locator("article");
    await expect(proposal.getByText("A follow-up remains in src/fragile.py")).toBeVisible();
    await proposal.getByText("Edit proposal").click();
    await proposal.getByLabel("Problem").fill("Confirm the client reliability follow-up.");
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
    const submissions: Array<{
        body: any;
        key: string | null;
    }> = [];
    await page.route("**/api/v1/**", async (route) => {
        const request = route.request();
        const url = new URL(request.url());
        const path = url.pathname;
        if (path === "/api/v1/events")
            return route.fulfill({ status: 204 });
        if (path === "/api/v1/overview")
            return route.fulfill({ json: { metrics: {}, active: [] } });
        if (path === "/api/v1/projects")
            return route.fulfill({ json: [{
                        name: "smoke-project", validate_commands: [], priorities: [], exclusions: [], protected_paths: [],
                        workspace_mode: "worktree", sandbox: "read-only", approval_policy: "on-request",
                    }] });
        if (path === "/api/v1/settings")
            return route.fulfill({ json: { revision: "smoke-revision",
                    global: { codex_bin: "codex", claude_bin: "claude", cursor_agent_bin: "cursor-agent",
                        codex_model: "gpt-test", codex_reasoning_effort: "high", opencode_enabled: false,
                        opencode_model: null, agent_runtime_timeout_seconds: 2700, agent_idle_timeout_seconds: 600,
                        max_concurrent_runs: 3 }, profiles: {}, projects: [] } });
        if (path === "/api/v1/execution-plans/deferred")
            return route.fulfill({ json: [] });
        if (path === "/api/v1/inbox")
            return route.fulfill({ json: [{
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
