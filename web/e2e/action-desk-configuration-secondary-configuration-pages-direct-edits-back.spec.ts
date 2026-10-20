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
test("secondary configuration pages direct edits back to the Action Desk", async ({ page }) => {
    await page.route("**/api/v1/**", async (route) => {
        const url = new URL(route.request().url());
        if (url.pathname === "/api/v1/events")
            return route.fulfill({ status: 204 });
        if (url.pathname === "/api/v1/settings")
            return route.fulfill({ json: settings });
        if (url.pathname === "/api/v1/projects")
            return route.fulfill({ json: [project] });
        if (url.pathname === "/api/v1/providers")
            return route.fulfill({ json: [] });
        if (url.pathname === "/api/v1/overview")
            return route.fulfill({ json: { metrics: {}, active: [] } });
        if (url.pathname === "/api/v1/execution-plans/deferred")
            return route.fulfill({ json: [] });
        if (url.pathname === "/api/v1/inbox")
            return route.fulfill({ json: [] });
        if (url.pathname === "/api/v1/projects/smoke-project")
            return route.fulfill({ json: { ...project, path: "/repo", working_tree: [] } });
        return route.fulfill({ status: 404, json: { error: { message: "Not found" } } });
    });
    for (const path of ["/projects", "/projects/detail?name=smoke-project", "/providers", "/settings"]) {
        await page.goto(path);
        await expect(page.getByRole("link", { name: /Action Desk|Overview/ }).first()).toBeVisible();
        await expect(page.getByRole("link", { name: /on the Action Desk/ }).first()).toBeVisible();
        await expect(page.locator("form")).toHaveCount(0);
    }
    await page.goto("/settings");
    await page.getByRole("link", { name: "Edit runtime settings on the Action Desk" }).click();
    await expect(page).toHaveURL(/\/#action-desk-configuration$/);
    await expect(page.locator("#action-desk-configuration")).toHaveAttribute("open", "");
});
