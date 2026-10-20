import { existsSync, mkdirSync, readFileSync, realpathSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { basename, dirname, join } from "node:path";
import type { Fixture } from "./fixtures/workspace";
import { expect, test } from "@playwright/test";
test("Settings startup snapshot survives external edits and UI YAML saves", async ({ page, request }, testInfo) => {
    const fixture: Fixture = JSON.parse(process.env.WHIRLWIND_E2E_FIXTURE!);
    const file = join(fixture.root, "settings.yaml");
    const original = readFileSync(file, "utf8");
    const snapshot = await (await request.get("/api/v1/settings/file")).json();
    const workerRecord = await (await request.get("/api/v1/settings")).json();
    const nextSlots = workerRecord.data.global_slots === 6 ? 7 : 6;
    const edited = original.replace(/global_slots: \d+/, `global_slots: ${nextSlots}`);
    const { writeFileSync } = await import("node:fs");
    try {
        writeFileSync(file, edited);
        await page.goto("/settings");
        await expect(page.getByLabel("Startup YAML", { exact: true })).toHaveText(snapshot.content);
        await page.getByRole("button", { name: "Edit file", exact: true }).click();
        await expect(page.getByLabel("YAML file contents", { exact: true })).toHaveValue(edited);
        const saved = "# Saved from YAML editor\n" + edited.replace(`global_slots: ${nextSlots}`, "global_slots: 8");
        await page.getByLabel("YAML file contents", { exact: true }).fill(saved);
        await page.getByRole("button", { name: "Save file", exact: true }).click();
        await expect.poll(() => readFileSync(file, "utf8")).toBe(saved);
        await expect(page.getByLabel("Startup YAML", { exact: true })).toHaveText(snapshot.content);
        await page.reload();
        await expect(page.getByLabel("Startup YAML", { exact: true })).toHaveText(snapshot.content);
        for (const [name, width, height] of [["desktop", 1440, 1000], ["mobile", 390, 844]] as const) {
            await page.setViewportSize({ width, height });
            expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
            await page.screenshot({ path: testInfo.outputPath(`settings-${name}.png`), fullPage: true });
            await page.getByRole("button", { name: "Edit file", exact: true }).click();
            await expect(page.getByLabel("YAML file contents", { exact: true })).toHaveValue(saved);
            await expect(page.getByRole("button", { name: "Save file", exact: true })).toBeDisabled();
            expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
            await page.screenshot({ path: testInfo.outputPath(`settings-editor-${name}.png`), fullPage: true });
            await page.getByRole("button", { name: "Back to snapshot", exact: true }).click();
        }
        writeFileSync(file, "workers: [invalid");
        await page.reload();
        await expect(page.getByLabel("Startup YAML", { exact: true })).toHaveText(snapshot.content);
        await page.getByRole("button", { name: "Edit file", exact: true }).click();
        await expect(page.getByLabel("YAML file contents", { exact: true })).toHaveValue("workers: [invalid");
        await page.getByLabel("YAML file contents", { exact: true }).fill(saved);
        await page.getByRole("button", { name: "Save file", exact: true }).click();
        await expect.poll(() => readFileSync(file, "utf8")).toBe(saved);
    }
    finally {
        writeFileSync(file, original);
    }
    await expect.poll(async () => (await (await request.get("/api/v1/settings")).json()).data.global_slots).toBe(workerRecord.data.global_slots);
});
