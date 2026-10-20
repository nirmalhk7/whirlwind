import { existsSync, mkdirSync, readFileSync, realpathSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { basename, dirname, join } from "node:path";
import type { Fixture } from "./fixtures/workspace";
import { expect, test } from "@playwright/test";
test("startup YAML includes configured providers", async ({ page }, testInfo) => {
    await page.goto("/providers");
    await expect(page.getByRole("heading", { name: "Settings", exact: true })).toBeVisible();
    await expect(page.getByLabel("Startup YAML", { exact: true })).toContainText("name: Antigravity");
    await expect(page.getByLabel("Startup YAML", { exact: true })).toContainText("kind: unavailable");
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.screenshot({ path: testInfo.outputPath("providers-desktop.png"), fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 });
    await page.screenshot({ path: testInfo.outputPath("providers-mobile.png"), fullPage: true });
});
test("Inbox provider count identifies the same working providers and failure reasons in Settings", async ({ page }) => {
    const providers = [
        { id: "one", name: "Codex", kind: "codex", status: "working", detail: "Account connection verified." },
        { id: "two", name: "Cursor", kind: "acp", status: "working", detail: "Protocol session responding." },
        { id: "three", name: "Claude", kind: "claude", status: "failed", detail: "Authentication required: run claude login." },
        { id: "four", name: "Missing", kind: "acp", status: "unavailable", detail: "Configured command executable not found." },
    ];
    await page.route("**/api/v1/**", async (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path.endsWith("/events"))
            return route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" });
        if (path.endsWith("/provider-status"))
            return route.fulfill({ json: { providers, interval_seconds: 30, stopped: false } });
        if (path.endsWith("/overview"))
            return route.fulfill({ json: { projects: [], tasks: [], directions: [], inbox: [], providers: providers.map(p => ({ id: p.id, version: 1, data: { ...p, available: true } })) } });
        if (path.endsWith("/settings/file"))
            return route.fulfill({ json: { path: "/workspace/settings.yaml", content: "version: 1\n", loaded_at: "2026-10-19T18:00:00Z" } });
        if (path.endsWith("/settings"))
            return route.fulfill({ json: { id: "global", version: 1, data: { stopped: false } } });
        if (path.endsWith("/runners/status"))
            return route.fulfill({ json: { processing: false } });
        return route.fulfill({ json: [] });
    });
    await page.goto("/inbox");
    const count = page.getByRole("link", { name: /working providers/ });
    await expect(count).toContainText("2 working providers");
    await count.click();
    await expect(page).toHaveURL(/\/settings#providers$/);
    const section = page.getByRole("region", { name: "Providers", exact: true });
    await expect(section).toContainText("2 working");
    for (const name of ["Codex", "Cursor"])
        await expect(section.getByRole("listitem").filter({ has: page.getByRole("heading", { name, exact: true }) })).toContainText("Working");
    await expect(section.getByRole("listitem").filter({ has: page.getByRole("heading", { name: "Claude", exact: true }) })).toContainText("Authentication required: run claude login.");
    await expect(section.getByRole("listitem").filter({ has: page.getByRole("heading", { name: "Missing", exact: true }) })).toContainText("Configured command executable not found.");
});
