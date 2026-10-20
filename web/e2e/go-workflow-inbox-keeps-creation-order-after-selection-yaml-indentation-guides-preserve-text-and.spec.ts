import { existsSync, mkdirSync, readFileSync, realpathSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { basename, dirname, join } from "node:path";
import type { Fixture } from "./fixtures/workspace";
import { expect, test } from "@playwright/test";
test("YAML indentation guides preserve text and track editing and scrolling", async ({ page }, testInfo) => {
    const original = "workers:\n  ideation:\n    enabled: true\n  stopped: true\n";
    let current = original;
    await page.route("**/api/v1/**", async (route) => {
        const url = new URL(route.request().url());
        if (url.pathname.endsWith("/events"))
            return route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" });
        if (url.pathname.endsWith("/settings/file")) {
            if (route.request().method() === "PATCH") {
                current = route.request().postDataJSON().content;
                return route.fulfill({ json: { saved: true } });
            }
            return route.fulfill({ json: { path: "/workspace/settings.yaml", content: url.searchParams.get("view") === "file" ? current : original, revision: "r1", loaded_at: "2026-10-19T12:00:00Z" } });
        }
        return route.fulfill({ json: [] });
    });
    await page.setViewportSize({ width: 668, height: 853 });
    await page.goto("/settings");
    const snapshot = page.getByLabel("Startup YAML", { exact: true });
    await expect(snapshot).toHaveText(original);
    await expect(snapshot.locator('.yaml-guide-line[data-line="2"] .yaml-indent-guide')).toHaveCount(2);
    await expect(snapshot.locator('.yaml-guide-line[data-line="0"] .yaml-indent-guide')).toHaveCount(0);
    await page.getByRole("button", { name: "Edit file", exact: true }).click();
    const editor = page.getByLabel("YAML file contents", { exact: true });
    const draft = original + Array.from({ length: 70 }, (_, i) => `  option_${i}: ${i === 30 ? "x".repeat(200) : "true"}`).join("\n") + "\n";
    await editor.fill(draft);
    await expect(editor).toHaveValue(draft);
    await expect(page.locator('.yaml-editor-guides .yaml-guide-line[data-line="2"] .yaml-indent-guide')).toHaveCount(2);
    await editor.evaluate(el => { el.scrollTop = 240; el.scrollLeft = 80; });
    await expect.poll(async () => page.locator(".yaml-editor-guides").evaluate(el => el.scrollTop)).toBe(240);
    await expect.poll(async () => page.locator(".yaml-editor-guides").evaluate(el => el.scrollLeft)).toBe(80);
    await page.getByRole("button", { name: "Save file", exact: true }).click();
    await expect(snapshot).toHaveText(original);
    expect(current).toBe(draft);
    for (const [name, width, height] of [["desktop", 668, 853], ["mobile", 390, 844]] as const) {
        await page.setViewportSize({ width, height });
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
        await page.screenshot({ path: testInfo.outputPath(`yaml-guides-${name}.png`), fullPage: true });
    }
});
