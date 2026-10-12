import { existsSync } from "node:fs";
import { join } from "node:path";
import type { Fixture } from "./fixtures/workspace";
import { expect, test } from "@playwright/test";

test("backend inventory and unavailable provider render", async ({ page }, testInfo) => {
  await page.goto("/providers");
  await expect(page.getByRole("heading", { name: "Providers", exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Antigravity", exact: true })).toBeVisible();
  await expect(page.getByText(/Consumer-plan third-party/)).toBeVisible();
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.screenshot({ path: testInfo.outputPath("providers-desktop.png"), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: testInfo.outputPath("providers-mobile.png"), fullPage: true });
});

test("planner answers and critical approval use current versions", async ({ page }) => {
  let answered = false, approved = false;
  const task = { id: "task-one", version: 4, data: { id: "task-one", project_id: "p", issue: { title: "OAuth", body: "Implement OAuth" }, stage: "questions", score: 9, plan_version: 1, plan: { objective: "OAuth", acceptance_criteria: ["Login succeeds"], questions: [{ id: "q", text: "Which callback?", answer: "" }], units: [] } } };
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith("/events")) { await route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" }); return; }
    if (url.pathname.endsWith("/answers")) {
      expect(route.request().headers()["if-match"]).toBe("4");
      expect(route.request().postDataJSON()).toEqual({ answers: { q: "http://localhost/callback" } });
      answered = true; task.version = 5; task.data.stage = "approval";
      await route.fulfill({ json: task }); return;
    }
    if (url.pathname.endsWith("/approve")) { expect(route.request().headers()["if-match"]).toBe("5"); approved = true; task.version = 6; task.data.stage = "pr_created"; Object.assign(task.data, { pr: "https://github.com/o/r/pull/1" }); await route.fulfill({ json: task }); return; }
    if (url.pathname.endsWith("/tasks")) { await route.fulfill({ json: [task] }); return; }
    if (url.pathname.endsWith("/reviews")) { await route.fulfill({ json: [] }); return; }
    if (url.pathname.endsWith("/overview")) { await route.fulfill({ json: { projects: [], tasks: [task], providers: [], directions: [], inbox: [] } }); return; }
    await route.fulfill({ json: [] });
  });
  await page.goto("/");
  await page.getByRole("button", { name: "Open task" }).click();
  await page.getByLabel("Which callback?").fill("http://localhost/callback");
  await page.getByRole("button", { name: "Submit answers" }).click();
  await expect.poll(() => answered).toBe(true);
  await page.getByRole("button", { name: "Approve PR publication" }).click();
  await expect.poll(() => approved).toBe(true);
  await expect(page.getByRole("link", { name: "Open pull request" })).toHaveAttribute("href", "https://github.com/o/r/pull/1");
});

test("trusted repository runs direction through verified PR with real backend", async ({ page, request }) => {
  test.setTimeout(60_000);
  const fixture: Fixture = JSON.parse(process.env.KHAN_E2E_FIXTURE!);
  await page.goto("/providers");
  await page.getByLabel("Provider ID").fill("fixture");
  await page.getByLabel("Display name").fill("Fixture agent");
  await page.getByLabel("Executable and arguments (JSON array)").fill(JSON.stringify([fixture.agent]));
  await page.getByRole("button", { name: "Register agent" }).click();
  await expect(page.getByRole("heading", { name: "Fixture agent", exact: true })).toBeVisible();
  await page.goto("/projects");
  await page.getByLabel("Project name").fill("Fixture project");
  await page.getByLabel("Repository path").fill(fixture.repo);
  await page.getByRole("button", { name: "Register project" }).click();
  await expect(page.getByRole("heading", { name: "Fixture project", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Select direction" }).first()).toBeVisible({ timeout: 20_000 });
  await page.getByRole("button", { name: "Select direction" }).first().click();
  await expect(page.getByRole("link", { name: "Open pull request" })).toHaveAttribute("href", "https://github.com/khan-fixture/demo/pull/1", { timeout: 40_000 });
  const response = await request.get("/api/v1/tasks");
  const tasks = await response.json();
  expect(tasks).toHaveLength(1);
  expect(tasks[0].data).toMatchObject({ stage: "pr_created", score: 9, checks_passed: true });
  expect(existsSync(join(fixture.repo, "result.txt"))).toBe(false);
  expect(existsSync(join(tasks[0].data.worktree, "result.txt"))).toBe(true);
});

test("native questions appear in Inbox and return answers", async ({ page }) => {
  let answered = false;
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/events")) { await route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" }); return; }
    if (path.endsWith("/overview")) { await route.fulfill({ json: { projects: [], providers: [], tasks: [], directions: [], inbox: [] } }); return; }
    if (path.endsWith("/questions")) { await route.fulfill({ json: answered ? [] : [{ id: "native", version: 1, data: { id: "native", provider: "codex", status: "pending", questions: [{ id: "db", text: "Which database?", options: ["SQLite", "PostgreSQL"] }] } }] }); return; }
    if (path.endsWith("/questions/native/answers")) { expect(route.request().headers()["if-match"]).toBe("1"); expect(route.request().postDataJSON()).toEqual({ answers: { db: "SQLite" } }); answered = true; await route.fulfill({ json: {} }); return; }
    await route.fulfill({ json: [] });
  });
  await page.goto("/inbox");
  await page.getByLabel("Which database?").fill("SQLite");
  await page.getByRole("button", { name: "Send answers to agent" }).click();
  await expect.poll(() => answered).toBe(true);
});
