import { expect, test, type Page } from "@playwright/test";

async function localPlanFixture(page: Page, options: { failedLoad?: boolean; staleApproval?: boolean } = {}) {
  const actions: { body: Record<string, unknown>; version: string | undefined }[] = [];
  let loads = 0;
  const task = {
    id: "local-plan", version: 7,
    data: {
      id: "local-plan", project_id: "project-one", mode: "agentic",
      issue: { title: "Improve build feedback", body: "Use CI evidence" },
      stage: "plan_approval", score: 0, review_passes: 0, plan_version: 2,
      selection: { candidate_id: "idea-one", objective: "Improve build feedback", scope: [], policy_hash: "policy", selected_at: "2026-10-22" },
      plan_document: { path: "/Users/test/.whirlwind/plans/project-one/local-plan/v2.md", hash: "document-hash" },
      plan: { objective: "Improve build feedback", acceptance_criteria: ["Build failure visible"], questions: [], units: [] },
    },
  };
  const content = "# Build feedback\n\nExplain failed builds.\n\n## Acceptance\n\n- [x] Preserve CI context\n- [ ] Show failures\n\nSource: [Internal CI](https://ci.example.test/build/14)\n\n<script>window.localPlanExecuted = true</script>\n\n<img src=x onerror=alert(1)>";
  await page.route("**/api/v1/**", async route => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith("/events")) return route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" });
    if (url.pathname.endsWith("/plan-document")) {
      loads++;
      if (options.failedLoad) return route.fulfill({ status: 503, json: { error: { message: "Plan file could not be read" } } });
      return route.fulfill({ json: { path: task.data.plan_document.path, hash: "document-hash", content, plan: task.data.plan } });
    }
    if (url.pathname.endsWith("/plan-review")) {
      const body = route.request().postDataJSON();
      actions.push({ body, version: route.request().headers()["if-match"] });
      if (options.staleApproval && actions.length === 1) return route.fulfill({ status: 409, json: { error: { message: "Plan document changed; reload before approving" } } });
      task.version++;
      task.data.stage = body.action === "approve" ? "coding" : body.action === "revise" ? "planning" : "cancelled";
      return route.fulfill({ json: task });
    }
    if (url.pathname.endsWith("/overview")) return route.fulfill({ json: { projects: [], tasks: [task], providers: [], directions: [], inbox: [] } });
    if (url.pathname.endsWith("/tasks")) return route.fulfill({ json: [task] });
    return route.fulfill({ json: [] });
  });
  await page.goto("/tasks");
  await page.getByRole("button", { name: "Open task", exact: true }).click();
  return { actions, content, task, loads: () => loads, recoverPlan: () => { options.failedLoad = false; } };
}

test("local plan safely renders Markdown and approves the displayed hash and task version", async ({ page }) => {
  const fixture = await localPlanFixture(page);
  const review = page.getByRole("region", { name: "Local plan review" });
  await expect(review.getByRole("heading", { name: "Build feedback", exact: true })).toBeVisible();
  await expect(review.getByRole("listitem")).toHaveCount(2);
  await expect(review.getByText(fixture.task.data.plan_document.path, { exact: true })).toBeVisible();
  await expect(review.getByRole("link", { name: "Internal CI" })).toHaveAttribute("href", "https://ci.example.test/build/14");
  await expect(review).toContainText("<script>window.localPlanExecuted = true</script>");
  await expect(review.locator("script, img")).toHaveCount(0);
  expect(await page.evaluate(() => (window as unknown as Record<string, unknown>).localPlanExecuted)).toBeUndefined();
  await review.getByRole("button", { name: "Approve plan", exact: true }).click();
  await expect.poll(() => fixture.actions.length).toBe(1);
  expect(fixture.actions[0]).toEqual({ body: { action: "approve", hash: "document-hash" }, version: "7" });
  await expect(review.getByRole("button", { name: "Approve plan", exact: true })).toHaveCount(0);
});

for (const action of ["revise", "reject"] as const) {
  test(`local plan ${action} uses current document and preserves revision feedback`, async ({ page }) => {
    const fixture = await localPlanFixture(page);
    const review = page.getByRole("region", { name: "Local plan review" });
    await expect(review.getByRole("heading", { name: "Build feedback", exact: true })).toBeVisible();
    if (action === "revise") await review.getByLabel("Revision feedback").fill("Include failed-build acceptance coverage");
    await review.getByRole("button", { name: action === "revise" ? "Request revision" : "Reject plan", exact: true }).click();
    await expect.poll(() => fixture.actions.length).toBe(1);
    expect(fixture.actions[0]).toEqual({ body: { action, hash: "document-hash", ...(action === "revise" ? { feedback: "Include failed-build acceptance coverage" } : {}) }, version: "7" });
  });
}

test("local plan load errors recover and stale approval leaves document review actionable", async ({ page }) => {
  const fixture = await localPlanFixture(page, { failedLoad: true, staleApproval: true });
  const review = page.getByRole("region", { name: "Local plan review" });
  await expect(review.getByRole("alert")).toContainText("Plan file could not be read");
  fixture.recoverPlan();
  await review.getByRole("button", { name: "Reload plan", exact: true }).click();
  await expect(review.getByRole("heading", { name: "Build feedback", exact: true })).toBeVisible();
  await review.getByRole("button", { name: "Approve plan", exact: true }).click();
  await expect(review.getByRole("alert")).toContainText("Plan document changed");
  await expect(review.getByRole("heading", { name: "Build feedback", exact: true })).toBeVisible();
  await expect(review.getByRole("button", { name: "Approve plan", exact: true })).toBeEnabled();
  await review.getByRole("button", { name: "Reload plan", exact: true }).click();
  await expect(review.getByRole("alert")).toHaveCount(0);
  await review.getByRole("button", { name: "Approve plan", exact: true }).click();
  await expect.poll(() => fixture.actions.length).toBe(2);
});

test("repair and submission evidence remains readable after terminal cancellation", async ({ page }) => {
  const task = {
    id: "repair-task", version: 8,
    data: {
      id: "repair-task", project_id: "project-one", mode: "agentic",
      issue: { title: "Repair CI feedback", body: "Show actionable failures" },
      stage: "repair", score: 7, review_passes: 1, plan_version: 1,
      selection: { candidate_id: "repair-idea", objective: "Show actionable failures", scope: [], policy_hash: "policy", selected_at: "2026-10-22" },
      handoff: { failing_tests: ["TestBuildFeedback: missing failure reason"] },
      pr: "",
    },
  };
  const review = { id: "repair-task:review", version: 1, data: { score: 7, summary: "Failure reason needs coverage", findings: [{ id: "missing-reason", file: "build.go", line: 12, message: "Include the failure reason", blocking: true, resolved: false }] } };
  await page.route("**/api/v1/**", async route => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/events")) return route.fulfill({ contentType: "text/event-stream", body: ": heartbeat\n\n" });
    if (path.endsWith("/overview")) return route.fulfill({ json: { projects: [], tasks: [task], providers: [], directions: [], inbox: [] } });
    if (path.endsWith("/tasks")) return route.fulfill({ json: [task] });
    if (path.endsWith("/reviews")) return route.fulfill({ json: [review] });
    return route.fulfill({ json: [] });
  });
  const openDetails = async () => {
    await page.goto("/tasks");
    await page.getByRole("button", { name: "Open task", exact: true }).click();
    return page.getByRole("region", { name: "Task details", exact: true });
  };
  let details = await openDetails();
  await expect(details).toContainText("Include the failure reason");
  await expect(details).toContainText("TestBuildFeedback: missing failure reason");
  task.version++;
  task.data.stage = "submitting";
  task.data.score = 9;
  task.data.review_passes = 2;
  review.data.findings[0].resolved = true;
  details = await openDetails();
  await expect(details.locator('[data-stage="submitting"]')).toBeVisible();
  task.version++;
  task.data.stage = "cancelled";
  task.data.pr = "https://forge.example/team/product/pull/17";
  details = await openDetails();
  await expect(details.locator('[data-stage="cancelled"]')).toBeVisible();
  await expect(details.getByRole("button", { name: /Pause task|Resume task|Adopt task objective|Approve PR publication/ })).toHaveCount(0);
  await expect(details.getByRole("link", { name: "Open pull request", exact: true })).toHaveAttribute("href", task.data.pr);
  await expect(details).toContainText("Include the failure reason");
});
