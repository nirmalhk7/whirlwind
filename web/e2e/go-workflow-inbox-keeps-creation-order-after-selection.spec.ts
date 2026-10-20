import { existsSync, mkdirSync, readFileSync, realpathSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { basename, dirname, join } from "node:path";
import type { Fixture } from "./fixtures/workspace";
import { expect, test } from "@playwright/test";
test("Inbox keeps creation order after selection and shows total selected ideas without project headings", async ({ page }, testInfo) => {
    const categories = ["expand", "optimize", "harden", "simplify"];
    const directions = ["old", "middle", "new"].flatMap((group, g) => categories.map((category, i) => ({ id: `${group}-${i}`, version: 1, created_order: 10 + g * 10 + i, updated_at: `2026-10-19T${10 + g * 2}:00:00Z`, data: { id: `${group}-${i}`, project_id: g === 1 ? "p2" : "p1", group_id: group, category, title: `${group} ${category}`, description: "Stable original position", day: "2026-10-19", selected: g === 1 && i === 0 } })));
    const task = { id: "direction-old-2", version: 1, data: { id: "direction-old-2", project_id: "p1", issue: { title: "old harden", body: "" }, stage: "planning", score: 0, review_passes: 0, plan_version: 1 } };
    let tasks: typeof task[] = [];
    await page.route("**/api/v1/**", async (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path.endsWith("/events"))
            return route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" });
        if (path.endsWith("/overview"))
            return route.fulfill({ json: { projects: [{ id: "p1", version: 1, data: { name: "Alpha" } }, { id: "p2", version: 1, data: { name: "Beta" } }], tasks, providers: [], directions: [...directions].reverse(), inbox: [{ id: "notice", version: 1, created_order: 25, updated_at: "2026-10-19T13:00:00Z", data: { kind: "ideation", message: "Between batches", resolved: false } }] } });
        if (path.endsWith("/directions/old-2/select")) {
            for (const idea of directions.filter(idea => idea.data.group_id === "old")) {
                idea.data.selected = idea.id === "old-2";
                idea.updated_at = "2027-01-01T00:00:00Z";
                idea.version++;
            }
            tasks = [task];
            return route.fulfill({ status: 202, json: task });
        }
        return route.fulfill({ json: [] });
    });
    await page.setViewportSize({ width: 668, height: 853 });
    await page.goto("/inbox");
    const feed = page.getByRole("region", { name: "Messages and ideas", exact: true });
    const entries = feed.locator(".inbox-feed-entry");
    for (const [i, text] of ["new expand", "Between batches", "middle expand", "old expand"].entries())
        await expect(entries.nth(i)).toContainText(text);
    await page.getByRole("button", { name: "Like idea: old harden", exact: true }).click();
    await expect(page.getByRole("region", { name: "Task details", exact: true })).toContainText("2 ideas selected for plan generation");
    for (const [i, text] of ["new expand", "Between batches", "middle expand", "old expand"].entries())
        await expect(entries.nth(i)).toContainText(text);
    await expect(feed.locator(".idea-group-header")).not.toContainText(["Alpha", "Beta", "Alpha"]);
    await page.getByRole("button", { name: "Refresh", exact: true }).click();
    for (const [i, text] of ["new expand", "Between batches", "middle expand", "old expand"].entries())
        await expect(entries.nth(i)).toContainText(text);
    for (const [name, width, height] of [["desktop", 668, 853], ["mobile", 390, 844]] as const) {
        await page.setViewportSize({ width, height });
        await page.screenshot({ path: testInfo.outputPath(`stable-inbox-${name}.png`), fullPage: true });
    }
    await page.reload();
    for (const [i, text] of ["new expand", "Between batches", "middle expand", "old expand"].entries())
        await expect(entries.nth(i)).toContainText(text);
});
