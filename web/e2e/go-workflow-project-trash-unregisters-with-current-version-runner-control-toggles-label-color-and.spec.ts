import { existsSync, mkdirSync, readFileSync, realpathSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { basename, dirname, join } from "node:path";
import type { Fixture } from "./fixtures/workspace";
import { expect, test } from "@playwright/test";
test("runner control toggles label color and persisted stop state", async ({ page }, testInfo) => {
    let stopped = false;
    await page.route("**/api/v1/**", async (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path.endsWith("/events"))
            return route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" });
        if (path.endsWith("/settings"))
            return route.fulfill({ json: { id: "global", version: 1, data: { stopped } } });
        if (path.endsWith("/settings/file"))
            return route.fulfill({ json: { path: "/workspace/settings.yaml", content: "workers:\n  stopped: true\n", loaded_at: "2026-10-19T12:00:00Z" } });
        if (path.endsWith("/runners/stop")) {
            stopped = true;
            return route.fulfill({ json: { paused: 0 } });
        }
        if (path.endsWith("/runners/resume")) {
            stopped = false;
            return route.fulfill({ json: { resumed: 0 } });
        }
        if (path.endsWith("/overview"))
            return route.fulfill({ json: { projects: [], providers: [], tasks: [], directions: [], inbox: [] } });
        return route.fulfill({ json: [] });
    });
    for (const path of ["/inbox", "/settings"]) {
        stopped = false;
        await page.goto(path);
        const button = page.locator(".command-stop-all");
        await expect(button).toHaveText("Stop all runners");
        const group = page.getByRole("group", { name: "Runner controls", exact: true });
        await expect(group.getByRole("button", { name: "Connected", exact: true })).toBeDisabled();
        await expect(group.getByRole("button")).toHaveCount(3);
        await expect(button).toBeEnabled();
        await expect(button).toHaveCSS("background-color", "rgb(180, 35, 54)");
        await button.click();
        await expect(button).toBeEnabled();
        await expect(button).toHaveText("Resume all runners");
        if (path === "/inbox")
            for (const [name, width, height] of [["desktop", 1440, 1000], ["mobile", 390, 844]] as const) {
                await page.setViewportSize({ width, height });
                expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
                await page.screenshot({ path: testInfo.outputPath(`runner-group-${name}.png`), fullPage: true });
            }
        await page.getByRole("heading", { level: 1 }).hover();
        await expect(button).toHaveCSS("background-color", "rgba(0, 0, 0, 0)");
        await page.reload();
        await expect(button).toBeEnabled();
        await expect(button).toHaveCSS("background-color", "rgba(0, 0, 0, 0)");
        await expect(button).toHaveText("Resume all runners");
        await button.click();
        await expect(button).toHaveText("Stop all runners");
        await expect(button).toBeEnabled();
        await page.getByRole("heading", { level: 1 }).hover();
        await expect(button).toHaveCSS("background-color", "rgb(180, 35, 54)");
    }
});
