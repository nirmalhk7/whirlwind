import { expect, test, type Page, type Route } from "@playwright/test";
import { planFixture, mockDeskApi } from './fixtures/desk-api';
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
    const submissions: Array<{
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
                        validate_commands: [], priorities: [], exclusions: [], protected_paths: [],
                        workspace_mode: "worktree", sandbox: "read-only", approval_policy: "on-request" }] });
        if (path === "/api/v1/settings")
            return route.fulfill({ json: { revision: "smoke-revision",
                    global: { codex_bin: "codex", claude_bin: "claude", cursor_agent_bin: "cursor-agent",
                        codex_model: "gpt-test", codex_reasoning_effort: "high", opencode_enabled: false,
                        opencode_model: null, agent_runtime_timeout_seconds: 2700, agent_idle_timeout_seconds: 600,
                        max_concurrent_runs: 3 }, profiles: {}, projects: [] } });
        if (path === "/api/v1/execution-plans/deferred")
            return route.fulfill({ json: [] });
        if (path === "/api/v1/inbox")
            return route.fulfill({ json: [{ run_id: record.id,
                        subject_type: "permission", classification: "decision_required", summary: record.summary,
                        evidence: ["provider=codex", "request=permission"], recommended_actions: ["Review requested access"] }] });
        if (path === `/api/v1/permissions/${record.id}` && request.method() === "GET")
            return route.fulfill({ json: record });
        if (path === `/api/v1/permissions/${record.id}/decisions` && request.method() === "POST") {
            submissions.push({ body: request.postDataJSON(), key: request.headers()["idempotency-key"] ?? null });
            record.status = "response_queued";
            record.record.status = "response_queued";
            record.record.revision = 2;
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
