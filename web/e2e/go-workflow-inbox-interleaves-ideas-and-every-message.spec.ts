import { existsSync, mkdirSync, readFileSync, realpathSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { basename, dirname, join } from "node:path";
import type { Fixture } from "./fixtures/workspace";
import { expect, test } from "@playwright/test";
test("Inbox interleaves ideas and every message type with local timestamps", async ({ page }, testInfo) => {
    const at = (hour: number) => `2026-10-19T${hour}:00:00Z`;
    await page.route("**/api/v1/**", async (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path.endsWith("/events"))
            return route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" });
        if (path.endsWith("/overview"))
            return route.fulfill({ json: { projects: [{ id: "p", version: 1, data: { name: "Project Alpha" } }], providers: [], tasks: [], directions: [
                        { id: "idea-new", version: 1, updated_at: at(14), data: { project_id: "p", title: "New idea", description: "Improve recovery", category: "harden", selected: false } },
                        { id: "idea-old", version: 1, updated_at: at(12), data: { project_id: "p", title: "Older idea", description: "Improve indexing", category: "optimize", selected: false } },
                    ], inbox: [{ id: "notice", version: 1, updated_at: at(13), data: { kind: "ideation", message: "Generation completed", resolved: false } }, { id: "legacy", version: 1, data: { kind: "startup", message: "Legacy message", resolved: false } }] } });
        if (path.endsWith("/questions"))
            return route.fulfill({ json: [{ id: "q", version: 1, updated_at: at(11), data: { provider: "codex", status: "pending", questions: [{ id: "answer", text: "Choose a direction" }] } }] });
        if (path.endsWith("/permissions"))
            return route.fulfill({ json: [{ id: "permission", version: 1, updated_at: at(15), data: { provider: "claude", decision: "pending", request: { tool: "Read" } } }] });
        return route.fulfill({ json: [] });
    });
    await page.goto("/inbox");
    const feed = page.getByRole("region", { name: "Messages and ideas", exact: true });
    const entries = feed.locator(".inbox-feed-entry");
    await expect(entries).toHaveCount(6);
    for (const [index, text] of ["claude requests permission", "New idea", "Generation completed", "Older idea", "Choose a direction", "Legacy message"].entries())
        await expect(entries.nth(index)).toContainText(text);
    await expect(feed.locator("time")).toHaveCount(5);
    for (const [index, hour] of [15, 14, 13, 12, 11].entries()) {
        await expect(entries.nth(index).locator("time")).toHaveAttribute("datetime", at(hour));
        await expect(entries.nth(index).locator("time")).not.toBeEmpty();
    }
    await expect(entries.nth(5)).toContainText("Timestamp unavailable");
    await expect(feed.locator(".idea-group-header").first()).not.toContainText("Project Alpha");
    for (const [name, width, height] of [["desktop", 1440, 1000], ["mobile", 390, 844]] as const) {
        await page.setViewportSize({ width, height });
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
        await page.screenshot({ path: testInfo.outputPath(`inbox-feed-${name}.png`), fullPage: true });
    }
});
