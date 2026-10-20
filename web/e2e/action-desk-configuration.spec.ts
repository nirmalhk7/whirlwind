import { expect, test } from "@playwright/test";
const project = {
    name: "smoke-project",
    validate_commands: ["pytest -q"],
    priorities: ["Keep decisions in the Action Desk"],
    exclusions: [],
    protected_paths: [".github/workflows"],
    workspace_mode: "worktree",
    sandbox: "read-only",
    approval_policy: "on-request",
    discovery_enabled: true,
    discovery_interval_minutes: 60,
};
const settings = {
    revision: "revision-1",
    global: {
        codex_bin: "/usr/local/bin/codex",
        claude_bin: "claude",
        cursor_agent_bin: "cursor-agent",
        codex_model: "gpt-test",
        codex_reasoning_effort: "high",
        opencode_enabled: false,
        opencode_model: null,
        agent_runtime_timeout_seconds: 2700,
        agent_idle_timeout_seconds: 600,
        max_concurrent_runs: 3,
    },
    profiles: {},
    projects: [project],
};
test("project, provider, and runtime changes are completable from the Action Desk", async ({ page }) => {
    const mutations: Array<{
        path: string;
        method: string;
        body: any;
        revision: string | null;
    }> = [];
    let currentRevision = settings.revision;
    await page.route("**/api/v1/**", async (route) => {
        const request = route.request();
        const url = new URL(request.url());
        if (url.pathname === "/api/v1/events")
            return route.fulfill({ status: 204 });
        if (url.pathname === "/api/v1/overview")
            return route.fulfill({ json: { metrics: {}, active: [] } });
        if (url.pathname === "/api/v1/execution-plans/deferred")
            return route.fulfill({ json: [] });
        if (url.pathname === "/api/v1/inbox")
            return route.fulfill({ json: [] });
        if (url.pathname === "/api/v1/projects" && request.method() === "GET") {
            return route.fulfill({ json: [project] });
        }
        if (url.pathname === "/api/v1/settings" && request.method() === "GET") {
            return route.fulfill({ json: { ...settings, revision: currentRevision } });
        }
        if (url.pathname === "/api/v1/providers") {
            return route.fulfill({ json: [
                    { provider: "codex", executable: "codex", available: true, version: "1.0", capabilities: [], limitations: [] },
                    { provider: "claude", executable: "claude", available: false, version: null, capabilities: [], limitations: [] },
                    { provider: "cursor-agent", executable: "cursor-agent", available: true, version: "1.0", capabilities: [], limitations: [] },
                ] });
        }
        if (["PATCH", "POST"].includes(request.method())) {
            mutations.push({
                path: url.pathname,
                method: request.method(),
                body: request.postDataJSON(),
                revision: request.headers()["if-match"] ?? null,
            });
            currentRevision = `revision-${Number(currentRevision.split("-").at(-1)) + 1}`;
            return route.fulfill({ json: { state: "succeeded", result: currentRevision } });
        }
        return route.fulfill({ status: 404, json: { error: { message: `Unexpected request: ${url.pathname}` } } });
    });
    await page.goto("/");
    await expect(page.getByRole("heading", { name: "Action Desk" })).toBeVisible();
    await page.getByText("Project and runtime setup").click();
    await page.getByLabel("Validation commands, one per line").fill("pytest -q\nruff check .");
    await page.getByLabel("Scan interval in minutes").fill("90");
    await page.getByRole("checkbox", { name: "Run deterministic scans while Whirlwind's daemon is active" }).uncheck();
    await page.getByRole("button", { name: "Save project direction and policy" }).click();
    await expect(page.getByRole("status").filter({ hasText: "Project direction and policy saved" })).toBeVisible();
    expect(mutations.at(-1)).toMatchObject({
        path: "/api/v1/projects/smoke-project",
        method: "PATCH",
        revision: '"revision-1"',
        body: { changes: {
                validate_commands: ["pytest -q", "ruff check ."],
                priorities: ["Keep decisions in the Action Desk"],
                exclusions: [], protected_paths: [".github/workflows"], workspace_mode: "worktree",
                sandbox: "read-only", approval_policy: "on-request",
                discovery_enabled: false, discovery_interval_minutes: 90,
            } },
    });
    await page.getByLabel("Codex model").fill("gpt-next");
    await page.getByRole("button", { name: "Save runtime settings" }).click();
    await expect(page.getByRole("status").filter({ hasText: "Runtime settings saved" })).toBeVisible();
    expect(mutations.at(-1)).toMatchObject({
        path: "/api/v1/settings",
        method: "PATCH",
        revision: '"revision-2"',
        body: { changes: { global: { codex_model: "gpt-next" } } },
    });
    await page.getByLabel("Codex executable").fill("/custom/codex");
    await page.getByRole("button", { name: "Refresh CLI status" }).click();
    await expect(page.getByLabel("Codex executable")).toHaveValue("/custom/codex");
    await page.getByRole("button", { name: "Save provider CLI paths" }).click();
    await expect(page.getByRole("status").filter({ hasText: "Provider paths saved" })).toBeVisible();
    expect(mutations.at(-1)).toMatchObject({
        revision: '"revision-3"',
        body: {
            changes: { global: { codex_bin: "/custom/codex" } },
        },
    });
    await page.getByLabel("New project name").fill("new-project");
    await page.getByLabel("Repository root path").fill("/tmp/new-project");
    await page.getByRole("button", { name: "Register project" }).click();
    await expect(page.getByRole("status").filter({ hasText: "Project registered" })).toBeVisible();
    expect(mutations.at(-1)).toMatchObject({
        path: "/api/v1/projects",
        method: "POST",
        revision: '"revision-4"',
        body: { name: "new-project", path: "/tmp/new-project", revision: "revision-4" },
    });
    await expect(page).toHaveURL(/\/$/);
});
