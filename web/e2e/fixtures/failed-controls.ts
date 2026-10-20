import type { Page, Route } from '@playwright/test';
export async function mockFailedControls(page: Page) {
    const records = new Map<string, any>([
        ["queue-failed", { id: "queue-failed", type: "queue", project: "smoke-project", status: "failed",
                summary: "Queue item failed", updated_at: "queue-rev-1", record: {}, evidence: { error: "worker exited" } }],
        ["run-failed", { id: "run-failed", type: "run", project: "smoke-project", status: "failed",
                summary: "Run failed", updated_at: "run-rev-1", record: {}, evidence: { error: "provider exited" } }],
        ["daemon-failed", { id: "daemon-failed", type: "daemon", project: "smoke-project", status: "failed",
                summary: "Queue daemon failed", updated_at: "daemon-rev-1", record: {}, evidence: { error: "worker stopped" } }],
        ["daemon-running", { id: "daemon-running", type: "daemon", project: "smoke-project", status: "running",
                summary: "Queue daemon running", updated_at: "daemon-rev-2", record: {}, evidence: { pid: 123 } }],
        ["run-active", { id: "run-active", type: "run", project: "smoke-project", status: "running",
                summary: "Active run", updated_at: "run-active-rev-1", record: { process_id: 321 }, evidence: {} }],
        ["queue-active", { id: "queue-active", type: "queue", project: "smoke-project", status: "queued",
                summary: "Queued item", updated_at: "queue-active-rev-1", record: {}, evidence: {} }],
        ["session-active", { id: "session-active", type: "session", project: "smoke-project", status: "running",
                summary: "Active session", updated_at: "session-active-rev-1", record: {}, evidence: {} }],
    ]);
    const cards = [...records.values()].map((record) => ({
        run_id: record.id, subject_type: record.type, classification: "stopped", summary: record.summary,
        evidence: [record.status], recommended_actions: ["Review current evidence and available actions."],
    }));
    const submitted: Array<{
        path: string;
        body: any;
        key: string | null;
    }> = [];
    const commonResponses: Record<string, any> = { "/api/v1/overview": { metrics: {}, active: [] },
        "/api/v1/projects": [{
                name: "smoke-project", validate_commands: [], priorities: [], exclusions: [], protected_paths: [],
                workspace_mode: "worktree", sandbox: "read-only", approval_policy: "on-request",
            }],
        "/api/v1/settings": {
            revision: "smoke-revision", global: { codex_bin: "codex", claude_bin: "claude", cursor_agent_bin: "cursor-agent",
                codex_model: "gpt-test", codex_reasoning_effort: "high", opencode_enabled: false, opencode_model: null,
                agent_runtime_timeout_seconds: 2700, agent_idle_timeout_seconds: 600, max_concurrent_runs: 3 },
            profiles: {}, projects: [{ name: "smoke-project", validate_commands: [], priorities: [], exclusions: [],
                    protected_paths: [], workspace_mode: "worktree", sandbox: "read-only", approval_policy: "on-request" }],
        },
        "/api/v1/providers": [],
        "/api/v1/execution-plans/deferred": [],
        "/api/v1/inbox": cards };
    await page.route("**/api/v1/**",route=>handleFailedControl(route,{records,submitted,commonResponses}));
    return { records, submitted };
}
function applyControl(records: Map<string, any>, path: string, body: any) {
    if (path === "/api/v1/daemons/start")
        records.get("daemon-failed").status = "running";
    if (path === "/api/v1/daemons/daemon-running/restarts")
        records.get("daemon-running").status = "running";
    if (path === "/api/v1/daemons/daemon-running/stops")
        records.get("daemon-running").status = "stopping";
    const activeRun = records.get("run-active");
    if (path === "/api/v1/executions/run-active/controls") {
        activeRun.status = body.action === "pause" ? "paused" : "running";
        activeRun.updated_at += `-${body.action}`;
    }
    if (path === "/api/v1/executions/run-active/cancellations")
        activeRun.status = "stopping";
    if (path === "/api/v1/executions/queue-active/cancellations")
        records.get("queue-active").status = "cancelled";
    if (path === "/api/v1/executions/session-active/cancellations")
        records.get("session-active").status = "stopping";
}

type FailedControlState={records:Map<string,any>;submitted:Array<{path:string;body:any;key:string|null}>;commonResponses:Record<string,any>};
async function handleFailedControl(route:Route,{records,submitted,commonResponses}:FailedControlState){
        const request = route.request();
        const url = new URL(request.url());
        const path = url.pathname;
        if (path === "/api/v1/events")
            return route.fulfill({ status: 204 });
        if (path in commonResponses)
            return route.fulfill({ json: commonResponses[path] });
        const recordMatch = path.match(/^\/api\/v1\/records\/([^/]+)$/);
        if (recordMatch && records.has(recordMatch[1]))
            return route.fulfill({ json: records.get(recordMatch[1]) });
        const controlPaths = new Set(["/api/v1/queue/queue-failed/requeues", "/api/v1/executions/run-failed/retries", "/api/v1/daemons/start", "/api/v1/daemons/daemon-running/restarts", "/api/v1/daemons/daemon-running/stops", "/api/v1/executions/run-active/controls", "/api/v1/executions/run-active/cancellations", "/api/v1/executions/queue-active/cancellations", "/api/v1/executions/session-active/cancellations", "/api/v1/sessions/session-active/steers"]);
        if (request.method() === "POST" && controlPaths.has(path)) {
            const body = request.postDataJSON() ?? {};
            submitted.push({ path, body, key: request.headers()["idempotency-key"] ?? null });
            applyControl(records, path, body);
            return route.fulfill({ json: {
                    state: "succeeded", id: `op-${submitted.length}`,
                    result: path === "/api/v1/sessions/session-active/steers" ? "queued-steer" : "queued-retry",
                } });
        }
        return route.fulfill({ status: 404, json: { error: { message: `Unexpected smoke-test request: ${path}` } } });
}
