import {installEventSource} from "./fixtures/event-source";
import { expect, test, type Page, type Route } from "@playwright/test";
import { planFixture, mockDeskApi } from './fixtures/desk-api';
test("SSE refresh preserves an unsent run reply and reports the latest revision on conflict", async ({ page }) => {
    const run: any = {
        id: "waiting-run", type: "run", project: "smoke-project", status: "needs_human",
        summary: "Run needs a clarification", updated_at: "rev-1", record: {},
        evidence: { question: "Old question" },
    };
    let replyBody: any = null;
    await installEventSource(page);
    await page.route("**/api/v1/**", async (route) => {
        const request = route.request();
        const path = new URL(request.url()).pathname;
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
                    profiles: {}, projects: [{ name: "smoke-project", validate_commands: [], priorities: [], exclusions: [],
                            protected_paths: [], workspace_mode: "worktree", sandbox: "read-only", approval_policy: "on-request" }],
                } });
        if (path === "/api/v1/providers")
            return route.fulfill({ json: [] });
        if (path === "/api/v1/execution-plans/deferred")
            return route.fulfill({ json: [] });
        if (path === "/api/v1/inbox")
            return route.fulfill({ json: [{
                        run_id: run.id, subject_type: "run", classification: "decision_required", summary: run.summary,
                        evidence: [run.status], recommended_actions: ["Answer the run"],
                    }] });
        if (path === `/api/v1/records/${run.id}`)
            return route.fulfill({ json: run });
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
