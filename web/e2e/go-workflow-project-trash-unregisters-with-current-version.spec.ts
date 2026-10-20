import { existsSync, mkdirSync, readFileSync, realpathSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { basename, dirname, join } from "node:path";
import type { Fixture } from "./fixtures/workspace";
import { expect, test } from "@playwright/test";
test("project trash unregisters with current version and preserves repository", async ({ page, request }, testInfo) => {
    const originalSettings = await (await request.get("/api/v1/settings")).json();
    const disabled = await request.patch("/api/v1/settings", {
        headers: { "if-match": String(originalSettings.version), "idempotency-key": randomUUID() },
        data: { ideation: { ...originalSettings.data.ideation, enabled: false } },
    });
    expect(disabled.status()).toBe(200);
    // This case deletes an idle project; scheduled ideation owns leases that correctly block deletion.
    try {
        const fixture: Fixture = JSON.parse(process.env.WHIRLWIND_E2E_FIXTURE!);
        const repo = join(fixture.root, "delete-project");
        mkdirSync(repo);
        execFileSync("git", ["-C", repo, "init", "-b", "main"]);
        for (const [key, value] of [["user.name", "Fixture"], ["user.email", "fixture@example.test"], ["commit.gpgsign", "false"], ["core.hooksPath", ".git/hooks"]])
            execFileSync("git", ["-C", repo, "config", key, value]);
        execFileSync("git", ["-C", repo, "remote", "add", "origin", "https://github.com/whirlwind-fixture/delete.git"]);
        await page.goto("/projects");
        await page.getByLabel("Repository path", { exact: true }).fill(repo);
        await page.getByRole("button", { name: "Register project", exact: true }).click();
        const card = page.locator("article").filter({ has: page.getByRole("heading", { name: "delete-project", exact: true }) });
        await expect(card).toBeVisible();
        const trash = card.getByRole("button", { name: "Delete project delete-project", exact: true });
        await expect(trash).toBeVisible();
        await page.setViewportSize({ width: 1440, height: 1000 });
        await page.screenshot({ path: testInfo.outputPath("projects-desktop.png"), fullPage: true });
        await page.setViewportSize({ width: 390, height: 844 });
        await page.screenshot({ path: testInfo.outputPath("projects-mobile.png"), fullPage: true });
        const cardBox = await card.boundingBox(), trashBox = await trash.boundingBox();
        expect(cardBox && trashBox && trashBox.x + trashBox.width > cardBox.x + cardBox.width - 8).toBeTruthy();
        const before = readFileSync(join(repo, ".whirlwind.yml"), "utf8");
        const registration = (await (await request.get("/api/v1/projects")).json()).find((p: {
            data: {
                path: string;
            };
        }) => p.data.path === realpathSync(repo));
        const deletion = page.waitForRequest(req => req.method() === "DELETE" && req.url().endsWith(`/projects/${registration.id}`));
        const deleted = page.waitForResponse(response => response.request().method() === "DELETE" && response.url().endsWith(`/projects/${registration.id}`));
        await trash.click();
        expect((await deletion).headers()["if-match"]).toBe(String(registration.version));
        expect((await deleted).status()).toBe(200);
        await expect(card).toHaveCount(0);
        await page.reload();
        await expect(page.getByRole("heading", { name: "delete-project", exact: true })).toHaveCount(0);
        expect(readFileSync(join(repo, ".whirlwind.yml"), "utf8")).toBe(before);
        expect((await request.get(`/api/v1/projects/${registration.id}`)).status()).toBe(404);
    }
    finally {
        const currentSettings = await (await request.get("/api/v1/settings")).json();
        const restored = await request.patch("/api/v1/settings", {
            headers: { "if-match": String(currentSettings.version), "idempotency-key": randomUUID() },
            data: { ideation: originalSettings.data.ideation },
        });
        expect(restored.status()).toBe(200);
    }
});
