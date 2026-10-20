import type { DeskHandlers } from './desk-api';
export const discoveryHandlers: DeskHandlers = {
    "POST /api/v1/projects/smoke-project/discovery-scans": async (route, state) => {
        const request = route.request();
        state.fixture.submissions.push({ kind: "discovery.scan", body: request.postDataJSON() });
        state.latestDiscoveryScan = {
            id: "discovery-scan-1", project: "smoke-project", trigger: "manual", status: "succeeded",
            head_commit: "abc123", direction: { priorities: ["reliability"], exclusions: [] },
            evidence: [
                { kind: "todo", path: "src/fragile.py", line: 4, title: "Follow-up marker" },
                { kind: "file_change", path: "src/pending.py", line: null, title: "Working-tree change" },
            ],
            evidence_digest: "digest-1", direction_digest: "direction-1", evidence_changed: true,
            scout_recommended: true, created_at: "2026-10-11T00:00:00Z",
        };
        await route.fulfill({ json: state.latestDiscoveryScan });
        return;
    },
    "GET /api/v1/projects/smoke-project/discovery-proposals": async (route, state) => {
        await route.fulfill({ json: state.proposals });
        return;
    },
    "POST /api/v1/projects/smoke-project/discovery-proposals": async (route, state) => {
        const request = route.request();
        state.fixture.submissions.push({ kind: "proposal.synthesize", body: request.postDataJSON() });
        state.proposals = [{ id: "proposal-1", project: "smoke-project", version: 1, status: "proposed",
                category: "reliability", problem: "A follow-up remains in src/fragile.py", benefit: "Clarify reliability behavior.",
                impact: "low", confidence: "low", uncertainty: ["Confirm relevance."], scope: ["src/fragile.py"],
                risks: [], dependencies: [], acceptance: ["Confirm the marker is still relevant."],
                recommended_next_action: "Review evidence.", evidence: [{ kind: "todo", path: "src/fragile.py", title: "Follow-up marker", line: 4 }] }];
        await route.fulfill({ json: state.proposals.map((proposal) => ({ ...proposal, inserted: true })) });
        return;
    },
    "PATCH /api/v1/discovery-proposals/proposal-1": async (route, state) => {
        const request = route.request();
        const body = request.postDataJSON();
        state.fixture.submissions.push({ kind: "proposal.revise", body });
        state.proposals = state.proposals.map((proposal) => proposal.id === "proposal-1"
            ? { ...proposal, ...body.changes, version: body.expected_version + 1 } : proposal);
        await route.fulfill({ json: state.proposals[0] });
        return;
    },
    "GET /api/v1/discovery-proposals/proposal-1/history": async (route, state) => {
        await route.fulfill({ json: state.proposals });
        return;
    },
    "POST /api/v1/discovery-proposals/proposal-1/approvals": async (route, state) => {
        const request = route.request();
        const body = request.postDataJSON();
        state.fixture.submissions.push({ kind: "proposal.approve", body, key: request.headers()["idempotency-key"] });
        state.proposals = state.proposals.map((proposal) => ({ ...proposal, status: "approved", version: body.expected_version + 1,
            planning_task_id: "planning-task-1" }));
        await route.fulfill({ json: { proposal: state.proposals[0], operation: { state: "succeeded" } } });
        return;
    }
};
