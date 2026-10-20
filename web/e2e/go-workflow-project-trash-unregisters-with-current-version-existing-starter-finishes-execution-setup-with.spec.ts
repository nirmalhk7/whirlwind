import { existsSync, mkdirSync, readFileSync, realpathSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { basename, dirname, join } from "node:path";
import type { Fixture } from "./fixtures/workspace";
import { expect, test } from "@playwright/test";
test("existing starter finishes execution setup with current version", async ({ page }) => {
    let ready = false;
    await page.route("**/api/v1/**", async (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path.endsWith("/events"))
            return route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" });
        if (path.endsWith("/overview"))
            return route.fulfill({ json: { projects: [{ id: "legacy", version: ready ? 2 : 1, data: { name: "Legacy", repo: "o/r", path: "/repo", policy_hash: ready ? "committed" : "" } }], providers: [], tasks: [], directions: [], inbox: [] } });
        if (path.endsWith("/projects/legacy/policy")) {
            expect(route.request().headers()["if-match"]).toBe("1");
            ready = true;
            return route.fulfill({ json: { id: "legacy", version: 2 } });
        }
        return route.fulfill({ json: [] });
    });
    await page.goto("/projects");
    await page.getByRole("button", { name: "Finish execution setup", exact: true }).click();
    await expect(page.getByText("Ready for execution", { exact: true })).toBeVisible();
    await expect(page.getByText("Ideation only · policy setup needed for execution", { exact: true })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Confirm committed policy", exact: true })).toBeVisible();
});
test("Settings replaces forms with startup file and retains legacy redirects", async ({ page }) => {
    await page.goto("/settings");
    await expect(page.getByLabel("Startup YAML", { exact: true })).toBeVisible();
    for (const name of ["Worker settings", "Agents"])
        await expect(page.getByRole("heading", { name, exact: true })).toHaveCount(0);
    await expect(page.getByRole("heading", { name: "Providers", exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Edit file", exact: true })).toBeVisible();
    await page.goto("/agents");
    await expect(page).toHaveURL(/\/settings#agents$/);
    await expect(page.getByLabel("Startup YAML", { exact: true })).toBeVisible();
    await page.goto("/providers");
    await expect(page).toHaveURL(/\/settings#providers$/);
    await expect(page.getByLabel("Startup YAML", { exact: true })).toBeVisible();
});
