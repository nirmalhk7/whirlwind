import { expect, test } from "@playwright/test";

test("memory can be recorded, corrected, retired, and inspected by revision", async ({ page }) => {
  const writes: Array<{ method: string; body: any }> = [];
  const now = "2026-10-11T00:00:00Z";
  const memory: any = {
    id: "memory-1", project: "demo", version: 1, kind: "failed_approach",
    title: "Preserve timeout errors", content: "Do not swallow timeouts.",
    source_refs: [{ kind: "assessment", id: "assessment-1", version: "2", outcome: "regressed" }],
    relevant_paths: ["src/client.py"], relevant_symbols: ["Client"], related_terms: ["timeout", "client"],
    outcome: "regressed", source_revision: "rev-1", validity: "active", created_at: now, updated_at: now,
  };
  const history: any[] = [];
  let memoryId = "";
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path === "/api/v1/projects") return route.fulfill({ json: [{ name: "demo" }] });
    if (path === "/api/v1/engineering-memories" && request.method() === "GET")
      return route.fulfill({ json: history.length ? [history.at(-1)] : [] });
    if (path === "/api/v1/engineering-memories" && request.method() === "POST") {
      const body = request.postDataJSON(); writes.push({ method: "create", body });
      memoryId = body.record.id;
      Object.assign(memory, body.record); history.push(structuredClone(memory));
      return route.fulfill({ status: 201, json: memory });
    }
    if (path === `/api/v1/engineering-memories/${memoryId}` && request.method() === "PATCH") {
      const body = request.postDataJSON(); writes.push({ method: "revise", body });
      Object.assign(memory, body.changes, { version: body.expected_version + 1, updated_at: now });
      history.push(structuredClone(memory)); return route.fulfill({ json: memory });
    }
    if (path === `/api/v1/engineering-memories/${memoryId}` && request.method() === "GET")
      return route.fulfill({ json: history });
    return route.fulfill({ status: 404, json: { error: { message: "Unexpected request" } } });
  });

  await page.goto("/memory");
  await expect(page.getByRole("heading", { name: "Engineering memory" })).toBeVisible();
  await page.getByLabel("Type").selectOption("failed_approach");
  await page.getByLabel("Title", { exact: true }).fill("Preserve timeout errors");
  await page.getByLabel("What should future work know?").fill("Do not swallow timeouts.");
  await page.getByLabel("Source revision").fill("rev-1");
  await page.getByLabel("Source reference ID").fill("assessment-1");
  await page.getByLabel("Relevant paths").fill("src/client.py");
  await page.getByLabel("Relevant symbols").fill("Client");
  await page.getByLabel("Related terms").fill("timeout, client");
  await page.getByRole("button", { name: "Save memory" }).click();
  await expect(page.getByText(/Memory saved as version 1/)).toBeVisible();
  expect(writes[0].body.record.source_refs[0].id).toBe("assessment-1");

  const editor = page.locator("article.action-card");
  await editor.getByLabel("Memory content").fill("Keep timeout errors typed and visible.");
  await editor.getByLabel("Source revision").last().fill("rev-2");
  await editor.getByRole("button", { name: "Save correction as v2" }).click();
  await expect(page.getByText(new RegExp(`Saved ${memoryId} as version 2`))).toBeVisible();
  expect(writes[1].body.expected_version).toBe(1);

  await editor.getByRole("button", { name: "Retire memory" }).click();
  await expect(page.getByText(new RegExp(`Saved ${memoryId} as version 3`))).toBeVisible();
  expect(writes[2].body).toEqual({ expected_version: 2, changes: { validity: "retired" } });
  await editor.getByRole("button", { name: "Show revision history" }).click();
  await expect(page.getByText(/v1 · active/)).toBeVisible();
  await expect(page.getByText(/v3 · retired/)).toBeVisible();
});
