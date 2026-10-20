import { existsSync, mkdirSync, readFileSync, realpathSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { basename, dirname, join } from "node:path";
import type { Fixture } from "./fixtures/workspace";
import { expect, test } from "@playwright/test";
test("clicking a cyan idea unselects it and preserves state after failures and reload", async ({ page }) => {
    let deselects = 0;
    const idea = { id: "toggle", version: 3, data: { project_id: "p", title: "Toggle idea", description: "Preserve useful work", selected: true } };
    await page.route("**/api/v1/**", async (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path.endsWith("/events"))
            return route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" });
        if (path.endsWith("/overview"))
            return route.fulfill({ json: { projects: [{ id: "p", version: 1, data: { name: "Project" } }], tasks: [], providers: [], directions: [idea], inbox: [] } });
        if (path.endsWith("/directions/toggle/deselect")) {
            expect(route.request().headers()["if-match"]).toBe("3");
            deselects++;
            if (deselects === 1)
                return route.fulfill({ status: 409, json: { error: { message: "Try unselecting again" } } });
            idea.data.selected = false;
            idea.version = 4;
            return route.fulfill({ json: idea });
        }
        if (path.endsWith("/directions/toggle/select")) {
            expect(route.request().headers()["if-match"]).toBe("4");
            idea.data.selected = true;
            idea.version = 5;
            return route.fulfill({ status: 202, json: { id: "direction-toggle", version: 1, data: {} } });
        }
        return route.fulfill({ json: [] });
    });
    await page.goto("/inbox");
    const card = page.locator(".idea-choice");
    await expect(card).toHaveAttribute("data-selected", "true");
    await card.getByRole("button", { name: "Unselect idea: Toggle idea", exact: true }).click();
    await expect(page.getByText("Try unselecting again", { exact: true })).toBeVisible();
    await expect(card).toHaveAttribute("data-selected", "true");
    await card.getByRole("button", { name: "Unselect idea: Toggle idea", exact: true }).click();
    await expect(card).toHaveAttribute("data-selected", "false");
    await page.reload();
    await expect(card).toHaveAttribute("data-selected", "false");
    await card.getByRole("button", { name: "Like idea: Toggle idea", exact: true }).focus();
    await page.keyboard.press("Enter");
    await expect(card).toHaveAttribute("data-selected", "true");
});
