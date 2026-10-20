import { existsSync, mkdirSync, readFileSync, realpathSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { basename, dirname, join } from "node:path";
import type { Fixture } from "./fixtures/workspace";
import { expect, test } from "@playwright/test";
test("runner indicator switches between Connected and Processing across pages", async ({ page }) => {
    let processing = false, offline = false;
    await page.route("**/api/v1/**", async (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path.endsWith("/events"))
            return route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" });
        if (offline)
            return route.fulfill({ status: 503, json: { error: { message: "Backend unavailable" } } });
        if (path.endsWith("/runners/status"))
            return route.fulfill({ json: { processing } });
        if (path.endsWith("/settings/file"))
            return route.fulfill({ json: { path: "/workspace/settings.yaml", content: "version: 1\n", loaded_at: "2026-10-19T12:00:00Z" } });
        if (path.endsWith("/overview"))
            return route.fulfill({ json: { projects: [], providers: [], directions: [], inbox: [], tasks: [{ id: "queued", version: 1, data: { stage: "planning" } }] } });
        return route.fulfill({ json: [] });
    });
    await page.goto("/inbox");
    const controls = page.getByRole("group", { name: "Runner controls", exact: true });
    await expect(controls.getByRole("button", { name: "Connected", exact: true })).toBeDisabled();
    processing = true;
    await controls.getByRole("button", { name: "Refresh", exact: true }).click();
    await expect(controls.getByRole("button", { name: "Processing", exact: true })).toBeDisabled();
    await page.getByRole("navigation").getByRole("link", { name: "Settings", exact: true }).click();
    await expect(controls.getByRole("button", { name: "Processing", exact: true })).toBeDisabled();
    processing = false;
    await controls.getByRole("button", { name: "Refresh", exact: true }).click();
    await expect(controls.getByRole("button", { name: "Connected", exact: true })).toBeDisabled();
    offline = true;
    await controls.getByRole("button", { name: "Refresh", exact: true }).click();
    await expect(controls.getByRole("button", { name: "Offline", exact: true })).toBeDisabled();
});
