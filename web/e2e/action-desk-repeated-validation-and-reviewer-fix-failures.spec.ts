import { repeatedFailureScenario } from "./fixtures/repeated-failure";
import { expect, test, type Page, type Route } from "@playwright/test";
import { planFixture, mockDeskApi } from './fixtures/desk-api';
test("repeated validation and Reviewer FIX failures are reviewable on the Action Desk", async ({ page }) => {
    const scenarios = [
        repeatedFailureScenario({ id: "validation-stop", summary: "Repeated validation failure; Whirlwind stopped for your direction.", revision: "validation-rev-1", fingerprint: "validation-abc123", phase: "failure", detail: "tests/check.sh failed twice with the same output.", artifact: "validation-2.json", reply: "Fix the assertion in tests/check.sh, then rerun the failed check." }),
        repeatedFailureScenario({ id: "review-stop", summary: "Reviewer repeated the same FIX findings; inspect retained work before continuing.", revision: "review-rev-1", fingerprint: "review-def456", phase: "reviewing", detail: "Reviewer FIX: missing boundary validation.", artifact: "review-2.md", reply: "Add the missing boundary validation and ask the reviewer to check again." }),
    ];
    const runs = new Map(scenarios.map(({ run }) => [run.id, run]));
    const replyBodies: Array<{
        run_id: string;
        body: any;
    }> = [];
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
                    profiles: {}, projects: [{ name: "smoke-project", validate_commands: [], priorities: [], exclusions: [], protected_paths: [],
                            workspace_mode: "worktree", sandbox: "read-only", approval_policy: "on-request" }],
                } });
        if (path === "/api/v1/providers")
            return route.fulfill({ json: [] });
        if (path === "/api/v1/execution-plans/deferred")
            return route.fulfill({ json: [] });
        if (path === "/api/v1/inbox")
            return route.fulfill({ json: [...runs.values()]
                    .filter((run) => run.status === "needs_human")
                    .map((run) => ({ run_id: run.id, subject_type: "run", classification: "decision_required",
                    summary: run.summary, evidence: [run.status], recommended_actions: ["Review the stopped run"] })) });
        const recordId = path.startsWith("/api/v1/records/") ? path.slice("/api/v1/records/".length) : "";
        if (runs.has(recordId))
            return route.fulfill({ json: runs.get(recordId) });
        const replyMatch = path.match(/^\/api\/v1\/executions\/([^/]+)\/replies$/);
        if (replyMatch && request.method() === "POST") {
            const run = runs.get(replyMatch[1]);
            if (!run)
                return route.fulfill({ status: 404, json: { error: { message: "Unknown run" } } });
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
