import { existsSync, mkdirSync, readFileSync, realpathSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { basename, dirname, join } from "node:path";
import type { Fixture } from "./fixtures/workspace";
import { expect, test } from "@playwright/test";
test("Inbox combines messages newest first and keeps badge current across pages", async ({ page }) => {
    let resolved = false, answered = false, decided = false;
    await page.route("**/api/v1/**", async (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path.endsWith("/events"))
            return route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" });
        const inbox = [
            { id: "question-alert", version: 1, data: { kind: "question", message: "Agent clarification awaits answer: question", resolved: false } },
            { id: "permission-alert", version: 1, data: { kind: "permission", message: "Tool permission awaits decision: permission", resolved: false } },
            { id: "notice", version: 1, updated_at: "2026-10-14T12:00:00Z", data: { kind: "ideation", message: "Fresh directions ready", resolved } },
        ];
        if (path.endsWith("/settings/file"))
            return route.fulfill({ json: { path: "/workspace/settings.yaml", content: "version: 1\n", loaded_at: "2026-10-19T12:00:00Z" } });
        if (path.endsWith("/inbox"))
            return route.fulfill({ json: inbox });
        if (path.endsWith("/overview"))
            return route.fulfill({ json: { projects: [], providers: [], tasks: [], directions: [], inbox } });
        if (path.endsWith("/questions"))
            return route.fulfill({ json: [{ id: "question", version: 2, updated_at: "2026-10-14T11:00:00Z", data: { provider: "codex", status: answered ? "answered" : "pending", questions: [{ id: "q", text: "Which direction?" }] } }] });
        if (path.endsWith("/permissions"))
            return route.fulfill({ json: [{ id: "permission", version: 3, updated_at: "2026-10-14T13:00:00Z", data: { provider: "claude", decision: decided ? "deny" : "pending", request: { tool: "Read" } } }] });
        if (path.endsWith("/inbox/notice")) {
            resolved = true;
            return route.fulfill({ json: {} });
        }
        if (path.endsWith("/questions/question/answers")) {
            expect(route.request().headers()["if-match"]).toBe("2");
            answered = true;
            return route.fulfill({ json: {} });
        }
        if (path.endsWith("/permissions/permission/decisions")) {
            expect(route.request().headers()["if-match"]).toBe("3");
            decided = true;
            return route.fulfill({ json: {} });
        }
        return route.fulfill({ json: [] });
    });
    await page.goto("/agents");
    const badge = page.getByRole("navigation").getByRole("link", { name: /^Inbox/ });
    await expect(badge).toHaveText("Inbox3");
    await badge.click();
    const feed = page.getByRole("region", { name: "Messages and ideas" });
    await expect(feed.locator(".action-card")).toHaveCount(3);
    await expect(feed.locator(".action-card").nth(0)).toContainText("claude requests permission");
    await expect(feed.locator(".action-card").nth(1)).toContainText("Fresh directions ready");
    await expect(feed.locator(".action-card").nth(2)).toContainText("Which direction?");
    await expect(page.getByRole("button", { name: "Mark resolved" })).toHaveCount(0);
    const dismiss = page.getByRole("button", { name: "Dismiss notification", exact: true });
    await expect(dismiss.locator('svg[data-icon="xmark"]')).toBeVisible();
    await dismiss.click();
    await expect(badge).toHaveText("Inbox2");
    await page.getByRole("button", { name: "Deny", exact: true }).click();
    await expect(badge).toHaveText("Inbox1");
    await page.getByLabel("Which direction?").fill("Explore reliability");
    await page.getByRole("button", { name: "Send answers to agent" }).click();
    await expect(badge).toHaveText("Inbox");
    await expect(feed).toContainText("No messages or ideas waiting.");
});
