import { existsSync, mkdirSync, readFileSync, realpathSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { basename, dirname, join } from "node:path";
import type { Fixture } from "./fixtures/workspace";
import { expect, test } from "@playwright/test";
test("idea card previews expand and whole card selects with cyan feedback", async ({ page }, testInfo) => {
    let attempts = 0;
    const description = Array.from({ length: 40 }, (_, i) => `word${i + 1}`).join(" ");
    const directions = ["expand", "optimize", "harden", "simplify"].map((category, i) => ({ id: `short-${i}`, version: 7, data: { project_id: "p", group_id: "preview-batch", category, title: `${category} direction`, description, selected: false } }));
    await page.route("**/api/v1/**", async (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path.endsWith("/events"))
            return route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" });
        if (path.endsWith("/overview"))
            return route.fulfill({ json: { projects: [{ id: "p", version: 1, data: { name: "Project", policy_hash: "" } }], tasks: [], providers: [], directions, inbox: [] } });
        if (path.includes("/directions/short-") && path.endsWith("/select")) {
            attempts++;
            if (attempts === 1)
                return route.fulfill({ status: 422, json: { error: { message: "Selection unavailable" } } });
            await new Promise(resolve => setTimeout(resolve, 400));
            for (const idea of directions)
                idea.data.selected = idea.id === path.split("/").at(-2);
            return route.fulfill({ status: 202, json: { id: "direction-short-0", version: 1, data: {} } });
        }
        return route.fulfill({ json: [] });
    });
    await page.goto("/inbox");
    const card = page.locator(".idea-choice").filter({ has: page.getByRole("heading", { name: "expand direction", exact: true }) });
    await expect(card.locator(".idea-description")).not.toContainText("word36");
    await expect(card.getByRole("button", { name: "Like idea: expand direction", exact: true })).toHaveText("");
    const expand = card.getByRole("button", { name: "Show full idea", exact: true });
    await expect(expand).toHaveText("");
    await expect(expand.locator("svg")).toBeVisible();
    await card.getByRole("button", { name: "Show full idea", exact: true }).click();
    await expect(card.locator(".idea-description")).toContainText("word40");
    expect(attempts).toBe(0);
    await card.getByRole("button", { name: "Show less", exact: true }).click();
    await card.getByRole("heading", { name: "expand direction", exact: true }).click({ force: true });
    await expect(page.getByText("Selection unavailable", { exact: true })).toBeVisible();
    await expect(card).toHaveAttribute("data-selected", "false");
    await card.locator(".idea-description").click({ force: true });
    await expect(card.getByRole("button", { name: "Liking: expand direction" })).toBeDisabled();
    await expect(card).toHaveCSS("background-color", "rgb(56, 189, 248)");
    await expect(card.getByRole("button", { name: "Unselect idea: expand direction" })).toBeEnabled();
    expect(attempts).toBe(2);
    await page.reload();
    await expect(card).toHaveCSS("background-color", "rgb(56, 189, 248)");
    const keyboardChoice = page.getByRole("button", { name: "Like idea: optimize direction", exact: true });
    await keyboardChoice.focus();
    await page.keyboard.press("Enter");
    await expect(page.getByRole("button", { name: "Unselect idea: optimize direction", exact: true })).toBeEnabled();
    expect(attempts).toBe(3);
    for (const [name, width, height] of [["desktop", 1440, 1000], ["mobile", 390, 844]] as const) {
        await page.setViewportSize({ width, height });
        await page.evaluate(() => window.scrollTo(0, 0));
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
        await page.screenshot({ path: testInfo.outputPath(`selected-ideas-${name}.png`), fullPage: true });
    }
});
