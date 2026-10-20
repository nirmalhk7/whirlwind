import { existsSync, mkdirSync, readFileSync, realpathSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { basename, dirname, join } from "node:path";
import type { Fixture } from "./fixtures/workspace";
import { expect, test } from "@playwright/test";
for (const reducedMotion of ["no-preference", "reduce"] as const) {
    test(`command feedback waits for acknowledgement and respects ${reducedMotion} motion`, async ({ page }, testInfo) => {
        await page.emulateMedia({ reducedMotion });
        await page.setViewportSize({ width: reducedMotion === "reduce" ? 390 : 668, height: 853 });
        const idea = { id: "command", version: 1, created_order: 1, data: { id: "command", project_id: "p", group_id: "commands", category: "expand", title: "Choose a direction", description: "Direct the agent with a deliberate choice.", selected: false } };
        const task = { id: "direction-command", version: 1, data: { id: "direction-command", project_id: "p", issue: { title: "Choose a direction", body: "" }, stage: "planning", score: 0, review_passes: 0, plan_version: 1 } };
        let release!: () => void, processing = false;
        const acknowledgement = new Promise<void>(resolve => { release = resolve; });
        await page.route("**/api/v1/**", async (route) => {
            const path = new URL(route.request().url()).pathname;
            if (path.endsWith("/events"))
                return route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" });
            if (path.endsWith("/runners/status"))
                return route.fulfill({ json: { processing } });
            if (path.endsWith("/overview"))
                return route.fulfill({ json: { projects: [{ id: "p", version: 1, data: { name: "Project" } }], tasks: idea.data.selected ? [task] : [], providers: [], directions: [idea], inbox: [] } });
            if (path.endsWith("/directions/command/select")) {
                await acknowledgement;
                idea.data.selected = true;
                idea.version++;
                return route.fulfill({ status: 202, json: task });
            }
            return route.fulfill({ json: [] });
        });
        await page.goto("/inbox");
        const card = page.locator(".idea-choice");
        await card.scrollIntoViewIfNeeded();
        const before = (await card.boundingBox())!;
        await card.getByRole("button", { name: "Like idea: Choose a direction", exact: true }).click();
        await expect(card.locator(".idea-pending-mark")).toHaveCSS("opacity", "1");
        await expect(card.locator(".idea-selected-mark")).toHaveCSS("opacity", "0");
        const pending = (await card.boundingBox())!;
        expect(pending.width).toBeCloseTo(before.width);
        expect(pending.height).toBeCloseTo(before.height);
        expect(pending.y).toBeCloseTo(before.y);
        release();
        await expect(card.locator(".idea-selected-mark")).toHaveCSS("opacity", "1");
        await expect(card.locator(".idea-pending-mark")).toHaveCSS("opacity", "0");
        await expect(page.getByRole("region", { name: "Task details", exact: true })).toContainText("1 idea selected for plan generation");
        const accepted = (await card.boundingBox())!;
        expect(accepted.height).toBeCloseTo(before.height);
        processing = true;
        await page.getByRole("button", { name: "Refresh", exact: true }).click();
        await expect(page.getByRole("button", { name: "Processing", exact: true })).toBeDisabled();
        if (reducedMotion === "reduce")
            expect(await page.locator(".connection-dot").evaluate(el => el.getAnimations({ subtree: true }).filter(animation => animation.playState === "running").length)).toBe(0);
        await page.screenshot({ path: testInfo.outputPath(`command-feedback-${reducedMotion}.png`), fullPage: true });
    });
}
