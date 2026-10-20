import { existsSync, mkdirSync, readFileSync, realpathSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { basename, dirname, join } from "node:path";
import type { Fixture } from "./fixtures/workspace";
import { expect, test } from "@playwright/test";
test("idea groups switch one choice and delete only their four ideas", async ({ page }, testInfo) => {
    const categories = ["expand", "optimize", "harden", "simplify"];
    let directions = ["a", "b"].flatMap(group => categories.map((category, index) => ({ id: `${group}${index}`, version: 1, updated_at: group === "a" ? "2026-10-19T14:00:00Z" : "2026-10-19T12:00:00Z", data: { id: `${group}${index}`, project_id: "p", group_id: group, category, title: `${group} ${category}`, description: "An improvement with preserved task history.", day: "2026-10-19", selected: index === 0 } })));
    let deletes = 0;
    await page.route("**/api/v1/**", async (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path.endsWith("/events"))
            return route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" });
        if (path.endsWith("/overview"))
            return route.fulfill({ json: { projects: [{ id: "p", version: 1, data: { name: "Project" } }], providers: [], tasks: [], directions, inbox: [{ id: "notice", version: 1, updated_at: "2026-10-19T13:00:00Z", data: { kind: "ideation", message: "Between batches", resolved: false } }] } });
        if (path.endsWith("/directions/a1/select")) {
            for (const idea of directions.filter(idea => idea.data.group_id === "a")) {
                idea.data.selected = idea.id === "a1";
                idea.version++;
            }
            return route.fulfill({ status: 202, json: { id: "direction-a1", version: 1, data: {} } });
        }
        if (path.endsWith("/directions/a0/group")) {
            deletes++;
            if (deletes === 1)
                return route.fulfill({ status: 409, json: { error: { message: "Group changed. Refresh and retry." } } });
            directions = directions.filter(idea => idea.data.group_id !== "a");
            return route.fulfill({ json: { deleted: 4 } });
        }
        return route.fulfill({ json: [] });
    });
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.goto("/inbox");
    const groups = page.locator(".idea-group");
    await expect(groups).toHaveCount(2);
    const first = groups.filter({ has: page.getByRole("heading", { name: "a expand", exact: true }) });
    await expect(first.locator(".idea-choice")).toHaveCount(4);
    await first.getByRole("button", { name: "Like idea: a optimize", exact: true }).click();
    await expect(first.locator('.idea-choice[data-selected="true"]')).toHaveCount(1);
    await expect(first.getByRole("button", { name: "Like idea: a expand", exact: true })).toBeEnabled();
    await expect(page.getByRole("button", { name: "Unselect idea: b expand", exact: true })).toBeEnabled();
    const remove = first.getByRole("button", { name: "Delete all 4 ideas", exact: true });
    await expect(remove.locator('svg[data-icon="xmark"]')).toBeVisible();
    const buttonBox = (await remove.boundingBox())!, cardBox = (await first.locator(".idea-choice").first().boundingBox())!;
    expect(buttonBox.x).toBeLessThanOrEqual(cardBox.x + 2);
    expect(buttonBox.y).toBeLessThan(cardBox.y);
    await remove.click();
    await expect(page.getByText("Group changed. Refresh and retry.", { exact: true })).toBeVisible();
    await expect(groups).toHaveCount(2);
    for (const [name, width, height] of [["desktop", 1440, 1000], ["mobile", 390, 844]] as const) {
        await page.setViewportSize({ width, height });
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
        await page.screenshot({ path: testInfo.outputPath(`idea-groups-${name}.png`), fullPage: true });
    }
    await remove.click();
    await expect(groups).toHaveCount(1);
    await expect(page.getByText("Between batches", { exact: true })).toBeVisible();
    await page.reload();
    await expect(groups).toHaveCount(1);
    await expect(page.getByRole("heading", { name: "b expand", exact: true })).toBeVisible();
});
