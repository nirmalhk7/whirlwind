import { existsSync, mkdirSync, readFileSync, realpathSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { basename, dirname, join } from "node:path";
import type { Fixture } from "./fixtures/workspace";
import { expect, test } from "@playwright/test";
test("Inbox shows four idea columns and lets an empty-policy project select an idea", async ({ page }, testInfo) => {
    let selected = false;
    const directions = ["expand", "optimize", "harden", "simplify"].map((category, index) => ({ id: `idea-${index}`, version: 7, data: { project_id: "voyager", group_id: "batch", category, title: `${category} proposal`, description: `Project-specific ${category} improvement.`, day: "2026-10-14", selected: false } }));
    const task = { id: "direction-idea-2", version: 1, data: { project_id: "voyager", issue: { title: "harden proposal", body: "Improve recovery" }, score: 0, plan_version: 1, review_passes: 0, stage: "paused", error: "Configure and commit project policy before planning." } };
    await page.route("**/api/v1/**", async (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path.endsWith("/events"))
            return route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" });
        if (path.endsWith("/overview"))
            return route.fulfill({ json: { projects: [{ id: "voyager", version: 1, data: { name: "VOYAGER", policy_hash: "" } }], providers: [], tasks: selected ? [task] : [], directions, inbox: [] } });
        if (path.endsWith("/directions/idea-2/select")) {
            expect(route.request().method()).toBe("POST");
            expect(route.request().headers()["if-match"]).toBe("7");
            selected = true;
            directions[2].data.selected = true;
            return route.fulfill({ status: 202, json: task });
        }
        return route.fulfill({ json: [] });
    });
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.goto("/inbox");
    const choices = page.getByRole("region", { name: "Messages and ideas" });
    await expect(choices.getByRole("article")).toHaveCount(4);
    const cards = await choices.getByRole("article").all();
    const positions = await Promise.all(cards.map((card) => card.boundingBox()));
    expect(positions.every((position, index) => Math.abs(position!.y - positions[0]!.y) < 2 && (index === 0 || position!.x > positions[index - 1]!.x))).toBe(true);
    await choices.getByRole("button", { name: "Like idea: harden proposal", exact: true }).click();
    await expect.poll(() => selected).toBe(true);
    await expect(choices.getByRole("article")).toHaveCount(4);
    await expect(choices.getByRole("button", { name: "Unselect idea: harden proposal", exact: true })).toBeEnabled();
    await page.screenshot({ path: testInfo.outputPath("ideas-desktop.png"), fullPage: true });
    await page.setViewportSize({ width: 668, height: 1000 });
    const compactPositions = await Promise.all(cards.map(card => card.boundingBox()));
    expect(compactPositions.every(position => Math.abs(position!.y - compactPositions[0]!.y) < 2)).toBe(true);
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(choices.getByRole("article")).toHaveCount(4);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    await page.screenshot({ path: testInfo.outputPath("ideas-mobile.png"), fullPage: true });
});
