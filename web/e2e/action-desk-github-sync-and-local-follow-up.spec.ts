import { expect, test, type Page, type Route } from "@playwright/test";
import { planFixture, mockDeskApi } from './fixtures/desk-api';
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
        if (path === "/api/v1/events")
            return route.fulfill({ status: 204 });
        if (path === "/api/v1/overview")
            return route.fulfill({ json: { metrics: {}, active: [] } });
        if (path === "/api/v1/projects")
            return route.fulfill({ json: [project()] });
        if (path === "/api/v1/settings" && request.method() === "GET")
            return route.fulfill({ json: settings() });
        if (path === "/api/v1/settings" && request.method() === "PATCH")
            return route.fulfill({ json: { state: "succeeded" } });
        if (path === "/api/v1/providers")
            return route.fulfill({ json: [] });
        if (path === "/api/v1/execution-plans/deferred")
            return route.fulfill({ json: [] });
        if (path === "/api/v1/inbox")
            return route.fulfill({ json: synced ? [githubCard] : [] });
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
        if (path === "/api/v1/records/github-item-1")
            return route.fulfill({ json: {
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
