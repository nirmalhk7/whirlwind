import { expect, test } from "@playwright/test";

test("Run learning shows recalled advice and retires it with its current version", async ({ page }) => {
  let retired = false;
  await page.route("**/api/v1/**", async route => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/events") && path === "/api/v1/events") return route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" });
    if (path === "/api/v1/overview") return route.fulfill({ json: { projects: [{ id: "p", data: { id: "p", name: "Demo" } }], providers: [], tasks: [], directions: [], inbox: [] } });
    if (path === "/api/v1/sessions") return route.fulfill({ json: [{ id: "run", version: 1, data: { attempt_id: "run", task: "t", project_id: "p", role: "code", provider: "codex", finished: true, success: true, learning: { recalled: [{ id: "lesson", version: 1, status: "tentative" }], created_ids: [], bytes: 240, estimated_tokens: 60, warnings: ["An invalid observation was ignored"] } } }] });
    if (path === "/api/v1/lessons/lesson/retire") {
      expect(route.request().method()).toBe("POST");
      expect(route.request().headers()["if-match"]).toBe("2");
      expect(route.request().headers()["idempotency-key"]).toBeTruthy();
      retired = true;
    }
    if (path.startsWith("/api/v1/lessons/lesson")) return route.fulfill({ json: { id: "lesson", version: retired ? 3 : 2, data: { id: "lesson", project_id: "p", role: "code", status: retired ? "retired" : "corroborated", trigger: "Changing response format", mistake: "Editing generated client", correction: "Update the schema first", paths: ["schema.json"], evidence_ids: [] } } });
    if (path.endsWith("/sessions/run/events")) return route.fulfill({ json: { items: [], next_after: 0 } });
    if (path.endsWith("/sessions/run/evidence")) return route.fulfill({ json: { items: [] } });
    if (path === "/api/v1/settings") return route.fulfill({ json: { version: 1, data: { stopped: true } } });
    if (path === "/api/v1/runners/status") return route.fulfill({ json: { processing: false } });
    return route.fulfill({ json: [] });
  });
  await page.goto("/runs");
  await page.getByRole("button", { name: "Inspect run", exact: true }).click();
  const learning = page.getByRole("region", { name: "Agent learning" });
  await expect(learning.getByText("Update the schema first", { exact: true })).toBeVisible();
  await expect(learning).toContainText("240 bytes");
  await expect(learning).toContainText("60 estimated tokens");
  await expect(learning).toContainText("Recalled v1 · tentative");
  await expect(learning).toContainText("An invalid observation was ignored");
  await learning.getByRole("button", { name: "Retire lesson", exact: true }).click();
  await expect(learning).toContainText("Current: retired");
  await expect(learning.getByRole("button", { name: "Retire lesson", exact: true })).toHaveCount(0);
});
