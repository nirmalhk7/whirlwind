import { expect, test, type Page, type Route } from "@playwright/test";
import { planFixture, mockDeskApi } from './fixtures/desk-api';
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
            return route.fulfill({ json: cards });
        const recordPath = path.match(/^\/api\/v1\/records\/([^/]+)$/);
        if (recordPath && records.has(recordPath[1]))
            return route.fulfill({ json: records.get(recordPath[1]) });
        if (path.endsWith("/diff")) {
            diffRequests.push(`${path}${url.search}`);
            return route.fulfill({ json: { patch: "diff --git a/src/example.py b/src/example.py\n+verified change" } });
        }
        if (path.endsWith("/adoption-preview"))
            return route.fulfill({ json: {
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
