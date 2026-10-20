import { expect, test, type Page, type Route } from "@playwright/test";
import { planFixture, mockDeskApi } from './fixtures/desk-api';
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
    const imports: Array<{
        path: string;
        key: string | null;
    }> = [];
    const refreshes: Array<{
        body: any;
        key: string | null;
    }> = [];
    await page.route("**/api/v1/**", async (route) => {
        const request = route.request();
        const path = new URL(request.url()).pathname;
        if (path === "/api/v1/events")
            return route.fulfill({ status: 204 });
        if (path === "/api/v1/overview")
            return route.fulfill({ json: { metrics: {}, active: [] } });
        if (path === "/api/v1/projects")
            return route.fulfill({ json: [{ name: "smoke-project",
                        validate_commands: ["pytest -q"], priorities: ["reliability"], exclusions: [], protected_paths: [],
                        workspace_mode: "worktree", sandbox: "read-only", approval_policy: "on-request",
                        github_sync_enabled: true, github_sync: { synced_at: issue.record.updated_at, issue_count: 1,
                            pull_request_count: 0 } }] });
        if (path === "/api/v1/settings")
            return route.fulfill({ json: { revision: "smoke-revision",
                    global: { codex_bin: "codex", claude_bin: "claude", cursor_agent_bin: "cursor-agent",
                        codex_model: "gpt-test", codex_reasoning_effort: "high", opencode_enabled: false,
                        opencode_model: null, agent_runtime_timeout_seconds: 2700, agent_idle_timeout_seconds: 600,
                        max_concurrent_runs: 3 }, profiles: {}, projects: [] } });
        if (path === "/api/v1/execution-plans/deferred")
            return route.fulfill({ json: [] });
        if (path === "/api/v1/inbox")
            return route.fulfill({ json: [{ run_id: issue.id,
                        subject_type: "github", classification: "watch", summary: issue.summary, score: 45,
                        evidence: [`url=${issue.record.url}`], recommended_actions: ["Import this issue as a local plan"],
                        primary_action: "import_github_issue" }] });
        if (path === `/api/v1/records/${issue.id}` && request.method() === "GET")
            return route.fulfill({ json: {
                    ...issue, issue_import: importedPlan ? { task_id: "local-plan-1",
                        source_updated_at: importedSourceUpdatedAt, source_changed: importedSourceUpdatedAt !== issue.record.updated_at,
                        plan_version: currentPlanVersion, plan_status: "draft" } : null,
                } });
        if (path === "/api/v1/records/local-plan-1" && request.method() === "GET")
            return route.fulfill({ json: {
                    id: "local-plan-1", type: "plan", project: "smoke-project", status: "draft",
                    summary: "GH #7: Fix login", updated_at: issue.record.updated_at,
                    plan_history: currentPlanVersion === 1 ? [{ version: 1, status: "draft",
                            objective: "Prepare a local plan for GitHub issue #7", acceptance_criteria: [], updated_at: issue.record.updated_at }] : [
                        { version: 1, status: "draft", objective: "Prepare a local plan for GitHub issue #7",
                            acceptance_criteria: ["Expired sessions return 401 and refresh tokens are rotated."],
                            updated_at: "2026-10-11T00:30:00Z",
                            issue_source: { number: 7, updated_at: "2026-10-11T00:00:00Z" } },
                        { version: 2, status: "draft", objective: "Prepare a refreshed local plan for GitHub issue #7",
                            acceptance_criteria: [], updated_at: issue.record.updated_at }
                    ],
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
