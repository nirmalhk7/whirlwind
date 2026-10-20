import type { DeskHandlers } from './desk-api';
export const baseHandlers: DeskHandlers = {
    "* /api/v1/events": async (route, state) => {
        await route.fulfill({ status: 204 });
        return;
    },
    "* /api/v1/overview": async (route, state) => {
        await route.fulfill({ json: { metrics: {}, active: [] } });
        return;
    },
    "* /api/v1/projects": async (route, state) => {
        const request = route.request();
        await route.fulfill({ json: [{
                    name: "smoke-project", validate_commands: [], priorities: [], exclusions: [],
                    protected_paths: [], workspace_mode: "worktree", sandbox: "read-only", approval_policy: "on-request",
                    latest_discovery_scan: state.latestDiscoveryScan,
                }] });
        return;
    },
    "* /api/v1/settings": async (route, state) => {
        const request = route.request();
        await route.fulfill({ json: {
                revision: "smoke-revision",
                global: { codex_bin: "codex", claude_bin: "claude", cursor_agent_bin: "cursor-agent",
                    codex_model: "gpt-test", codex_reasoning_effort: "high", opencode_enabled: false,
                    opencode_model: null, agent_runtime_timeout_seconds: 2700, agent_idle_timeout_seconds: 600,
                    max_concurrent_runs: 3 },
                profiles: {},
                projects: [{ name: "smoke-project", validate_commands: [], priorities: [], exclusions: [],
                        protected_paths: [], workspace_mode: "worktree", sandbox: "read-only", approval_policy: "on-request" }],
            } });
        return;
    },
    "* /api/v1/providers": async (route, state) => {
        await route.fulfill({ json: [] });
        return;
    },
    "* /api/v1/execution-plans/deferred": async (route, state) => {
        await route.fulfill({ json: state.fixture.deferredPlans });
        return;
    },
    "* /api/v1/inbox": async (route, state) => {
        const url = new URL(route.request().url());
        const rows = state.inboxItems ?? (state.fixture.plan.status === "draft" ? [{
                run_id: "smoke-task", subject_type: "plan", classification: "decision_required",
                summary: state.fixture.plan.summary, evidence: ["Draft plan v" + state.fixture.plan.record.version],
                recommended_actions: ["Review the plan"],
            }] : []);
        const offset = Number(url.searchParams.get("offset") ?? "0");
        await route.fulfill({ json: rows.slice(offset, offset + 200) });
        return;
    },
    "GET /api/v1/records/smoke-task": async (route, state) => {
        await route.fulfill({ json: state.fixture.plan });
        return;
    }
};
