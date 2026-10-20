import type { DeskHandlers } from './desk-api';
export const planHandlers: DeskHandlers = {
    "POST /api/v1/execution-plans/smoke-task/deferrals": async (route, state) => {
        const request = route.request();
        const body = request.postDataJSON();
        state.fixture.submissions.push({ kind: "defer", body, key: request.headers()["idempotency-key"] });
        state.fixture.plan.status = "deferred";
        state.fixture.deferredPlans.push({ task_id: "smoke-task", project: "smoke-project", title: "Draft plan",
            version: body.version, objective: state.fixture.plan.record.content.objective, updated_at: "2026-10-11T00:01:00Z" });
        await route.fulfill({ json: { state: "succeeded", id: "op-defer" } });
        return;
    },
    "POST /api/v1/execution-plans/smoke-task/restorations": async (route, state) => {
        const request = route.request();
        const body = request.postDataJSON();
        state.fixture.submissions.push({ kind: "restore", body, key: request.headers()["idempotency-key"] });
        state.fixture.plan.status = "draft";
        state.fixture.deferredPlans.splice(0);
        await route.fulfill({ json: { state: "succeeded", id: "op-restore" } });
        return;
    },
    "POST /api/v1/execution-plans/smoke-task/answers": async (route, state) => {
        const request = route.request();
        const body = request.postDataJSON();
        state.fixture.submissions.push({ kind: "answer", body });
        state.fixture.plan.record.version = body.version + 1;
        state.fixture.plan.record.content.unresolved_requirements = [];
        state.fixture.plan.evidence = { plan: { version: state.fixture.plan.record.version, content: state.fixture.plan.record.content } };
        await route.fulfill({ json: { state: "succeeded" } });
        return;
    },
    "POST /api/v1/execution-plans/smoke-task/edits": async (route, state) => {
        const request = route.request();
        const body = request.postDataJSON();
        state.fixture.submissions.push({ kind: "edit", body });
        if (state.editStatus !== 200) {
            if (state.fixture.plan.record.version <= body.version) {
                state.fixture.plan.record.version = body.version + 1;
                state.fixture.plan.record.content.objective = "Current objective from another reviewer";
                state.fixture.plan.summary = "Plan changed elsewhere";
                state.fixture.plan.updated_at = "2026-10-11T00:05:00Z";
                state.fixture.plan.evidence = { plan: { version: state.fixture.plan.record.version, content: state.fixture.plan.record.content } };
            }
            await route.fulfill({ status: state.editStatus, json: { error: { message: "Execution plan version is stale." } } });
            return;
        }
        state.fixture.plan.record.version = body.version + 1;
        state.fixture.plan.record.content = { ...state.fixture.plan.record.content, ...body, unresolved_requirements: [] };
        state.fixture.plan.evidence = { plan: { version: state.fixture.plan.record.version, content: state.fixture.plan.record.content } };
        await route.fulfill({ json: { state: "succeeded" } });
        return;
    }
};
