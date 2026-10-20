import { existsSync, mkdirSync, readFileSync, realpathSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { basename, dirname, join } from "node:path";
import type { Fixture } from "./fixtures/workspace";
import { expect, test } from "@playwright/test";
test("Settings shows startup YAML and preserves edits after save errors", async ({ page }) => {
    const startup = { path: "/workspace/custom/settings.yaml", content: "# Startup\nversion: 1\nworkers: {global_slots: 4}\nproviders: []\nagents: {}\n", loaded_at: "2026-10-19T12:00:00Z" };
    let content = startup.content.replace("global_slots: 4", "global_slots: 6");
    let attempts = 0;
    await page.route("**/api/v1/**", async (route) => {
        const url = new URL(route.request().url());
        if (url.pathname.endsWith("/events"))
            return route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" });
        if (url.pathname.endsWith("/settings/file")) {
            if (route.request().method() === "PATCH") {
                const input = route.request().postDataJSON();
                expect(input.revision).toBe("file-revision");
                expect(input.content).toContain("global_slots: 8");
                attempts++;
                if (attempts === 1)
                    return route.fulfill({ status: 422, json: { error: { message: "Invalid worker capacity in settings.yaml" } } });
                content = input.content;
                return route.fulfill({ json: { saved: true } });
            }
            return route.fulfill({ json: url.searchParams.get("view") === "file" ? { path: startup.path, content, revision: "file-revision" } : startup });
        }
        return route.fulfill({ json: [] });
    });
    await page.goto("/settings");
    await expect(page.getByLabel("Startup YAML", { exact: true })).toHaveText(startup.content);
    await page.getByRole("button", { name: "Edit file", exact: true }).click();
    const editor = page.getByLabel("YAML file contents", { exact: true });
    await expect(editor).toHaveValue(content);
    await editor.fill(content.replace("global_slots: 6", "global_slots: 8"));
    await page.getByRole("button", { name: "Save file", exact: true }).click();
    await expect(page.locator(".yaml-settings").getByRole("alert")).toContainText("Invalid worker capacity");
    await expect(editor).toHaveValue(content.replace("global_slots: 6", "global_slots: 8"));
    await page.getByRole("button", { name: "Save file", exact: true }).click();
    await expect(page.getByLabel("Startup YAML", { exact: true })).toHaveText(startup.content);
    await page.getByRole("button", { name: "Refresh", exact: true }).click();
    await expect(page.getByLabel("Startup YAML", { exact: true })).toHaveText(startup.content);
    await page.getByRole("button", { name: "Edit file", exact: true }).click();
    await expect(editor).toHaveValue(content);
});
