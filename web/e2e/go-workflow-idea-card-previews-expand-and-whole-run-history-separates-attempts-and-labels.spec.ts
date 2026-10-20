import { existsSync, mkdirSync, readFileSync, realpathSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { basename, dirname, join } from "node:path";
import type { Fixture } from "./fixtures/workspace";
import { expect, test } from "@playwright/test";
test("Run history separates attempts and labels historical unknowns", async ({ page }, testInfo) => {
    const sessions = [
        { id: "old", version: 1, updated_at: "2026-10-14T10:00:00Z", data: { role: "ideate", provider: "deleted", task: "ideation:p", model: "", finished: true } },
        { id: "failed", version: 2, updated_at: "2026-10-14T11:00:01Z", data: { role: "code", provider: "codex", provider_name: "Codex", project_id: "p", task: "t", model: "configured", transport: "acp", started_at: "2026-10-14T11:00:00Z", elapsed_ms: 1000, finished: true, success: false, error: "Quota exhausted" } },
        { id: "success", version: 2, updated_at: "2026-10-14T12:00:01Z", data: { role: "code", provider: "cursor", provider_name: "Cursor", project_id: "p", task: "t", model: "configured", resolved_model: "actual-model", thinking: { config_id: "effort", value: "high" }, effective_thinking: { config_id: "effort", value: "high" }, transport: "acp", started_at: "2026-10-14T12:00:00Z", elapsed_ms: 1234, finished: true, success: true } },
    ];
    await page.route("**/api/v1/**", async (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path.endsWith("/events"))
            return route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" });
        if (path.endsWith("/overview"))
            return route.fulfill({ json: { projects: [{ id: "p", version: 1, data: { name: "Project" } }], providers: [], tasks: [], directions: [], inbox: [] } });
        if (path.endsWith("/sessions"))
            return route.fulfill({ json: sessions });
        return route.fulfill({ json: [] });
    });
    await page.goto("/inbox");
    await page.getByRole("navigation").getByRole("link", { name: "Run", exact: true }).click();
    await expect(page).toHaveURL(/\/runs$/);
    const rows = page.getByRole("region", { name: "Execution history" }).getByRole("article");
    await expect(rows).toHaveCount(3);
    await expect(rows.nth(0)).toContainText("actual-model");
    await expect(rows.nth(0)).toContainText("high");
    await expect(rows.nth(0)).toContainText("Succeeded");
    await expect(rows.nth(1)).toContainText("configured (configured)");
    await expect(rows.nth(1)).toContainText("Quota exhausted");
    await expect(rows.nth(2)).toContainText("Finished — outcome unknown");
    await expect(rows.nth(2)).toContainText("Unknown");
    for (const [name, width, height] of [["desktop", 1440, 1000], ["mobile", 390, 844]] as const) {
        await page.setViewportSize({ width, height });
        await page.evaluate(() => window.scrollTo(0, 0));
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
        await page.screenshot({ path: testInfo.outputPath(`runs-${name}.png`), fullPage: true });
    }
    sessions.push({ id: "plan", version: 1, updated_at: "2026-10-14T13:00:00Z", data: { role: "plan", provider: "cursor", task: "t", model: "m", finished: false } });
    sessions.push({ id: "review", version: 1, updated_at: "2026-10-14T14:00:00Z", data: { role: "review", provider: "cursor", task: "t", model: "m", finished: true } });
    await page.getByRole("button", { name: "Refresh", exact: true }).click();
    await expect(rows).toHaveCount(5);
    await expect(rows.nth(0)).toContainText("Reviewer Agent");
    await expect(rows.nth(1)).toContainText("Planner Agent");
});
