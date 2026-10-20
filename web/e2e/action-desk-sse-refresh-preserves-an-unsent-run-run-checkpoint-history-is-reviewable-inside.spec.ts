import { expect, test, type Page, type Route } from "@playwright/test";
import { planFixture, mockDeskApi } from './fixtures/desk-api';
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
                    revision: "smoke-revision", global: {}, profiles: {}, projects: [],
                } });
        if (path === "/api/v1/providers")
            return route.fulfill({ json: [] });
        if (path === "/api/v1/execution-plans/deferred")
            return route.fulfill({ json: [] });
        if (path === "/api/v1/inbox")
            return route.fulfill({ json: [{
                        run_id: run.id, subject_type: "run", classification: "decision_required",
                        summary: run.summary, evidence: ["needs_human"], recommended_actions: ["Review checkpoint"],
                    }] });
        if (path === `/api/v1/records/${run.id}`)
            return route.fulfill({ json: run });
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
