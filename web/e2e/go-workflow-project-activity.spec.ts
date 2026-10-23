import { expect, test } from "@playwright/test";

test("project card pauses and resumes with current version and survives reload", async ({ page }, testInfo) => {
  let paused = false, version = 4;
  const actions: string[] = [];
  await page.route("**/api/v1/**", async route => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/events")) return route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" });
    const project = { id: "p", version, data: { id: "p", name: "basera", repo: "nirmalhk7/basera", path: "/Users/nirmalhk7/Documents/DevWorld/basera", policy_hash: "confirmed", paused } };
    if (path.endsWith("/overview")) return route.fulfill({ json: { projects: [project], tasks: [], directions: [], providers: [], inbox: [] } });
    if (/\/projects\/p\/(pause|resume)$/.test(path)) {
      expect(route.request().headers()["if-match"]).toBe(String(version));
      expect(route.request().headers()["idempotency-key"]).toBeTruthy();
      actions.push(path.split("/").at(-1)!);
      paused = path.endsWith("/pause"); version++;
      return route.fulfill({ json: { ...project, version, data: { ...project.data, paused } } });
    }
    return route.fulfill({ json: [] });
  });
  await page.goto("/projects");
  const card = page.locator("article").filter({ has: page.getByRole("heading", { name: "basera", exact: true }) });
  await card.getByRole("button", { name: "Pause project basera", exact: true }).click();
  await expect(card.getByText("Activity paused", { exact: true })).toBeVisible();
  await expect(card.getByRole("button", { name: "Resume project basera", exact: true })).toBeEnabled();
  await page.reload();
  await expect(card.getByText("Activity paused", { exact: true })).toBeVisible();
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 900 });
    await page.screenshot({ path: testInfo.outputPath(`project-paused-${width}.png`), fullPage: true });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
  }
  await card.getByRole("button", { name: "Resume project basera", exact: true }).click();
  await expect(card.getByText("Activity paused", { exact: true })).toHaveCount(0);
  await expect(card.getByRole("button", { name: "Pause project basera", exact: true })).toBeEnabled();
  expect(actions).toEqual(["pause", "resume"]);
});

test("failed project pause preserves state and disables duplicate requests", async ({ page }) => {
  let release!: () => void;
  const gate = new Promise<void>(resolve => { release = resolve; });
  let calls = 0;
  await page.route("**/api/v1/**", async route => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/events")) return route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" });
    if (path.endsWith("/overview")) return route.fulfill({ json: { projects: [{ id: "p", version: 1, data: { name: "basera", repo: "o/r", path: "/repo" } }], tasks: [], directions: [], providers: [], inbox: [] } });
    if (path.endsWith("/pause")) {
      calls++; await gate;
      return route.fulfill({ status: 409, json: { error: { message: "Project changed. Refresh and retry." } } });
    }
    return route.fulfill({ json: [] });
  });
  await page.goto("/projects");
  const pause = page.getByRole("button", { name: "Pause project basera", exact: true });
  await pause.click();
  await expect(pause).toBeDisabled();
  release();
  await expect(page.getByText("Project changed. Refresh and retry.", { exact: true })).toBeVisible();
  await expect(pause).toBeEnabled();
  expect(calls).toBe(1);
});
