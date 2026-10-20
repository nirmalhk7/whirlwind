import { candidatePreview } from "./fixtures/candidate-preview";
import { expect, test, type Page, type Route } from "@playwright/test";
import { planFixture, mockDeskApi } from './fixtures/desk-api';
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
        if (path === "/api/v1/events")
            return route.fulfill({ status: 204 });
        if (path === "/api/v1/overview")
            return route.fulfill({ json: { metrics: {}, active: [] } });
        if (path === "/api/v1/projects")
            return route.fulfill({ json: [{ name: "smoke-project", validate_commands: [],
                        priorities: [], exclusions: [], protected_paths: ["secrets/"], workspace_mode: "worktree",
                        sandbox: "read-only", approval_policy: "on-request" }] });
        if (path === "/api/v1/settings")
            return route.fulfill({ json: { revision: "smoke-revision", global: {}, profiles: {}, projects: [] } });
        if (path === "/api/v1/providers")
            return route.fulfill({ json: [] });
        if (path === "/api/v1/execution-plans/deferred")
            return route.fulfill({ json: [] });
        if (path === "/api/v1/inbox")
            return route.fulfill({ json: cards });
        const recordPath = path.match(/^\/api\/v1\/records\/([^/]+)$/);
        if (recordPath && records.has(recordPath[1]))
            return route.fulfill({ json: records.get(recordPath[1]) });
        if (path.endsWith("/diff"))
            return route.fulfill({ json: { patch: "+protected edit" } });
        if (path.endsWith("/adoption-preview")) {
            return route.fulfill({ json: candidatePreview(path) });
        }
        if (path === "/api/v1/decisions" && request.method() === "POST") {
            const body = request.postDataJSON();
            submitted.push(body);
            if (body.id === "protected-candidate")
                return route.fulfill({ status: 409, json: { error: { message: "Candidate changed since preview." } } });
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
