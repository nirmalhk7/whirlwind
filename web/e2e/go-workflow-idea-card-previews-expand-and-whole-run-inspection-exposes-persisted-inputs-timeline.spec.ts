import { existsSync, mkdirSync, readFileSync, realpathSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { basename, dirname, join } from "node:path";
import type { Fixture } from "./fixtures/workspace";
import { expect, test } from "@playwright/test";
test("Run inspection exposes persisted inputs timeline and truncation", async ({ page }) => {
    await page.route("**/api/v1/**", async (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path === "/api/v1/events")
            return route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" });
        if (path.endsWith("/overview"))
            return route.fulfill({ json: { projects: [], providers: [], tasks: [], directions: [], inbox: [] } });
        if (path === "/api/v1/sessions")
            return route.fulfill({ json: [{ id: "attempt", version: 2, data: { attempt_id: "attempt", task: "t", role: "code", provider: "codex", finished: true, status: "interrupted", prompt_artifact: "prompt", usage: { scope: "session", total_tokens: 123 } } }] });
        if (path.endsWith("/sessions/attempt/events"))
            return route.fulfill({ json: { items: [{ sequence: 1, kind: "prompt", replay: false, created_at: "2026-10-14T10:00:00Z", artifact: { id: "prompt", kind: "prompt", truncated: true, bytes: 200, stored_bytes: 100 } }], next_after: 1 } });
        if (path.endsWith("/artifacts/prompt/content"))
            return route.fulfill({ contentType: "text/plain", body: "exact stored prompt" });
        return route.fulfill({ json: [] });
    });
    await page.goto("/runs");
    await expect(page.getByText("Interrupted", { exact: true })).toBeVisible();
    await page.getByRole("button", { name: "Inspect run", exact: true }).click();
    await expect(page.getByText("Truncated: 100 of 200 bytes", { exact: true })).toBeVisible();
    await page.getByRole("button", { name: "Show prompt", exact: true }).click();
    await expect(page.getByText("exact stored prompt", { exact: true })).toBeVisible();
    await expect(page.getByText("Session cumulative · 123 tokens · Cost unknown", { exact: true })).toBeVisible();
});
