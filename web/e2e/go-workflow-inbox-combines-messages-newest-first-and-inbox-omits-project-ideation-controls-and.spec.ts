import { existsSync, mkdirSync, readFileSync, realpathSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { basename, dirname, join } from "node:path";
import type { Fixture } from "./fixtures/workspace";
import { expect, test } from "@playwright/test";
test("Inbox omits project ideation controls and retains its message feed", async ({ page }) => {
    let queued = false;
    await page.route("**/api/v1/**", async (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path.endsWith("/events"))
            return route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" });
        if (path.endsWith("/overview"))
            return route.fulfill({ json: { projects: [{ id: "voyager", version: 1, data: { name: "VOYAGER", repo: "o/r", path: "/voyager" } }], providers: [], tasks: [], directions: [], inbox: [] } });
        if (path.endsWith("/projects/voyager/ideation")) {
            expect(route.request().method()).toBe("POST");
            queued = true;
            return route.fulfill({ status: 202, json: { operation_id: "generation" } });
        }
        if (path.endsWith("/operations/generation"))
            return route.fulfill({ json: { id: "generation", version: 2, data: { stage: "failed", error: "Provider authentication required" } } });
        return route.fulfill({ json: [] });
    });
    await page.goto("/inbox");
    await expect(page.getByRole("region", { name: "Messages and ideas" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Project ideation" })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Run ideation for VOYAGER" })).toHaveCount(0);
    expect(queued).toBe(false);
});
