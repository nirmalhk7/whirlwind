import { existsSync, mkdirSync, readFileSync, realpathSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { basename, dirname, join } from "node:path";
import type { Fixture } from "./fixtures/workspace";
import { expect, test } from "@playwright/test";
test("project deletion failure keeps card and reports error", async ({ page }) => {
    let release!: () => void;
    const responseGate = new Promise<void>(resolve => { release = resolve; });
    let deletes = 0;
    await page.route("**/api/v1/**", async (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path.endsWith("/events"))
            return route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" });
        if (path.endsWith("/overview"))
            return route.fulfill({ json: { projects: [{ id: "busy", version: 2, data: { name: "Busy project", repo: "o/r", path: "/repo" } }], tasks: [], directions: [], providers: [], inbox: [] } });
        if (route.request().method() === "DELETE") {
            deletes++;
            await responseGate;
            return route.fulfill({ status: 409, json: { error: { message: "Stop active work before deleting project." } } });
        }
        return route.fulfill({ json: [] });
    });
    await page.goto("/projects");
    const trash = page.getByRole("button", { name: "Delete project Busy project", exact: true });
    await trash.focus();
    await trash.press("Enter");
    await expect(trash).toBeDisabled();
    await trash.press("Enter");
    release();
    await expect(page.getByText("Stop active work before deleting project.", { exact: true })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Busy project", exact: true })).toBeVisible();
    await expect(trash).toBeEnabled();
    expect(deletes).toBe(1);
});
