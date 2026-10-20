import { existsSync, mkdirSync, readFileSync, realpathSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { basename, dirname, join } from "node:path";
import type { Fixture } from "./fixtures/workspace";
import { expect, test } from "@playwright/test";
test("native questions appear in Inbox and return answers", async ({ page }) => {
    let answered = false;
    await page.route("**/api/v1/**", async (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path.endsWith("/events")) {
            await route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" });
            return;
        }
        if (path.endsWith("/overview")) {
            await route.fulfill({ json: { projects: [], providers: [], tasks: [], directions: [], inbox: [] } });
            return;
        }
        if (path.endsWith("/questions")) {
            await route.fulfill({ json: answered ? [] : [{ id: "native", version: 1, data: { id: "native", provider: "codex", status: "pending", questions: [{ id: "db", text: "Which database?", options: ["SQLite", "PostgreSQL"] }] } }] });
            return;
        }
        if (path.endsWith("/questions/native/answers")) {
            expect(route.request().headers()["if-match"]).toBe("1");
            expect(route.request().postDataJSON()).toEqual({ answers: { db: "SQLite" } });
            answered = true;
            await route.fulfill({ json: {} });
            return;
        }
        await route.fulfill({ json: [] });
    });
    await page.goto("/inbox");
    await page.getByLabel("Which database?").fill("SQLite");
    await page.getByRole("button", { name: "Send answers to agent" }).click();
    await expect.poll(() => answered).toBe(true);
});
test("opening Whirlwind lands in Inbox without Workspace navigation", async ({ page }) => {
    await page.goto("/");
    await expect(page).toHaveURL(/\/inbox$/);
    const navigation = page.getByRole("navigation", { name: "Main navigation" });
    await expect(navigation.getByRole("link", { name: /^Inbox/ })).toHaveAttribute("aria-current", "page");
    await expect(navigation.getByRole("link", { name: "Workspace", exact: true })).toHaveCount(0);
    await expect(page.getByRole("heading", { name: "Ideas in. Verified software out.", exact: true })).toBeVisible();
});
test("folder selection fills repository path and cancellation preserves it", async ({ page }) => {
    const fixture: Fixture = JSON.parse(process.env.WHIRLWIND_E2E_FIXTURE!);
    await page.goto("/projects");
    await page.getByRole("button", { name: "Choose folder", exact: true }).click();
    const picker = page.getByRole("dialog", { name: "Select repository folder" });
    await expect(picker).toBeVisible();
    await picker.getByLabel("Folder location").fill(dirname(fixture.repo));
    await picker.getByRole("button", { name: "Go", exact: true }).click();
    await picker.getByRole("button", { name: basename(fixture.repo), exact: true }).click();
    await picker.getByRole("button", { name: "Use this folder", exact: true }).click();
    await expect(picker).not.toBeVisible();
    await expect(page.getByLabel("Repository path", { exact: true })).toHaveValue(realpathSync(fixture.repo));
    await expect(page.getByLabel("Project name", { exact: true })).toHaveValue("repo");
    await page.getByLabel("Repository path", { exact: true }).fill(join(fixture.root, "another-project") + "/");
    await expect(page.getByLabel("Project name", { exact: true })).toHaveValue("another-project");
    await page.getByLabel("Project name", { exact: true }).fill("Custom project");
    await page.getByLabel("Repository path", { exact: true }).fill(realpathSync(fixture.repo));
    await expect(page.getByLabel("Project name", { exact: true })).toHaveValue("Custom project");
    await page.getByRole("button", { name: "Choose folder", exact: true }).click();
    await expect(picker).toBeVisible();
    await picker.getByRole("button", { name: "Home", exact: true }).click();
    await picker.getByRole("button", { name: "Cancel", exact: true }).click();
    await expect(page.getByLabel("Repository path", { exact: true })).toHaveValue(realpathSync(fixture.repo));
    await expect(page.getByLabel("Project name", { exact: true })).toHaveValue("Custom project");
});
test("new project creates committed policy and onboards for execution", async ({ page, request }) => {
    const fixture: Fixture = JSON.parse(process.env.WHIRLWIND_E2E_FIXTURE!);
    const repo = join(fixture.root, "starter-project");
    mkdirSync(repo);
    execFileSync("git", ["-C", repo, "init", "-b", "main"]);
    execFileSync("git", ["-C", repo, "config", "user.name", "Fixture"]);
    execFileSync("git", ["-C", repo, "config", "user.email", "fixture@example.test"]);
    execFileSync("git", ["-C", repo, "config", "commit.gpgsign", "false"]);
    execFileSync("git", ["-C", repo, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "--allow-empty", "-m", "Fixture"]);
    execFileSync("git", ["-C", repo, "remote", "add", "origin", "https://github.com/whirlwind-fixture/starter.git"]);
    await page.goto("/projects");
    await page.getByLabel("Repository path", { exact: true }).fill(repo);
    await page.getByRole("button", { name: "Register project", exact: true }).click();
    await expect(page.getByRole("heading", { name: "starter-project", exact: true })).toBeVisible();
    await expect(page.locator("article").filter({ has: page.getByRole("heading", { name: "starter-project", exact: true }) }).getByText("Ready for execution", { exact: true })).toBeVisible();
    expect(readFileSync(join(repo, ".whirlwind.yml"), "utf8")).toContain("qlty");
    expect(execFileSync("git", ["-C", repo, "show", "HEAD:.whirlwind.yml"], { encoding: "utf8" })).toBe(readFileSync(join(repo, ".whirlwind.yml"), "utf8"));
    expect(execFileSync("git", ["-C", repo, "show", "--format=", "--name-only", "HEAD"], { encoding: "utf8" }).trim()).toBe(".qlty/qlty.toml\n.whirlwind.yml");
    expect(existsSync(join(repo, ".qlty/qlty.toml"))).toBe(true);
    expect(existsSync(join(repo, ".codegraph/codegraph.db"))).toBe(true);
    const projects = await (await request.get("/api/v1/projects")).json();
    expect(projects.some((project: {
        data: {
            path: string;
            policy_hash: string;
        };
    }) => project.data.path === realpathSync(repo) && project.data.policy_hash !== "")).toBe(true);
});
