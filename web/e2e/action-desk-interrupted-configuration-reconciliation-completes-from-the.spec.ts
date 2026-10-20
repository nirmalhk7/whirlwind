import { expect, test, type Page, type Route } from "@playwright/test";
import { planFixture, mockDeskApi } from './fixtures/desk-api';
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
    let submitted: {
        body: any;
        key: string | null;
    } | null = null;
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
            return route.fulfill({ json: {
                    revision: "smoke-revision", global: { codex_bin: "codex", claude_bin: "claude", cursor_agent_bin: "cursor-agent",
                        codex_model: "gpt-test", codex_reasoning_effort: "high", opencode_enabled: false, opencode_model: null,
                        agent_runtime_timeout_seconds: 2700, agent_idle_timeout_seconds: 600, max_concurrent_runs: 3 },
                    profiles: {}, projects: [],
                } });
        if (path === "/api/v1/providers")
            return route.fulfill({ json: [] });
        if (path === "/api/v1/execution-plans/deferred")
            return route.fulfill({ json: [] });
        if (path === "/api/v1/inbox")
            return route.fulfill({ json: [...needsAttention].map((id) => {
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
            if (!operation)
                return route.fulfill({ status: 404, json: { error: { message: "Unknown operation" } } });
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
