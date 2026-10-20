import { existsSync, mkdirSync, readFileSync, realpathSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { basename, dirname, join } from "node:path";
import type { Fixture } from "./fixtures/workspace";
import { expect, test } from "@playwright/test";
test("Settings provider status refreshes on configured interval without losing YAML edits", async ({ page }, testInfo) => {
    let checks = 0;
    let seconds = 30;
    let failed = false;
    let connectionFailed = false;
    await page.route("**/api/v1/provider-status", async (route) => {
        checks++;
        if (failed)
            return route.fulfill({ status: 503, json: { error: { message: "Status service unavailable" } } });
        await route.fulfill({ json: { interval_seconds: seconds, stopped: false, providers: [
                    { id: "codex", name: "Codex", kind: "codex", status: connectionFailed ? "failed" : "working", detail: connectionFailed ? "Authentication required" : "Connection verified", checked_at: "2026-10-19T18:00:00Z" },
                    { id: "missing", name: "Missing provider", kind: "acp", status: "unavailable", detail: "Command not found", checked_at: "2026-10-19T18:00:00Z" },
                ] } });
    });
    await page.clock.install();
    await page.goto("/settings");
    const section = page.getByRole("region", { name: "Providers", exact: true });
    await expect(section).toContainText("Working");
    await expect(section).toContainText("Missing provider");
    await expect(section).toContainText("Every 30 seconds");
    const initialChecks = checks;
    await page.getByRole("button", { name: "Edit file", exact: true }).click();
    const editor = page.getByLabel("YAML file contents", { exact: true });
    await editor.fill("# Keep my unsaved changes\n");
    await page.clock.fastForward(29000);
    expect(checks).toBe(initialChecks);
    seconds = 60;
    connectionFailed = true;
    await page.clock.fastForward(1000);
    await expect(section).toContainText("Authentication required");
    await expect(section).toContainText("Every 60 seconds");
    await expect(editor).toHaveValue("# Keep my unsaved changes\n");
    await page.clock.fastForward(30000);
    expect(checks).toBe(initialChecks + 1);
    failed = true;
    await page.clock.fastForward(30000);
    await expect(section).toContainText("Status service unavailable");
    await expect(section).toContainText("Last results may be outdated");
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.screenshot({ path: testInfo.outputPath("provider-status-desktop.png"), fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 });
    await page.screenshot({ path: testInfo.outputPath("provider-status-mobile.png"), fullPage: true });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});
