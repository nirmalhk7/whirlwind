import type { DeskHandlers } from './desk-api';
export const campaignHandlers: DeskHandlers = {
    "GET /api/v1/projects/smoke-project/campaigns": async (route, state) => {
        await route.fulfill({ json: state.campaigns });
        return;
    },
    "POST /api/v1/projects/smoke-project/campaigns": async (route, state) => {
        const request = route.request();
        const body = request.postDataJSON();
        state.fixture.submissions.push({ kind: "campaign.create", body });
        state.campaignId = body.id;
        state.campaigns = [body];
        await route.fulfill({ json: body });
        return;
    },
    "POST /api/v1/campaigns/:campaign/approvals": async (route, state) => {
        const request = route.request();
        const body = request.postDataJSON();
        state.fixture.submissions.push({ kind: "campaign.approve", body });
        state.campaigns = state.campaigns.map((campaign) => ({ ...campaign, status: "approved", version: body.expected_version + 1,
            approved_digest: "a".repeat(64) }));
        await route.fulfill({ json: state.campaigns[0] });
        return;
    },
    "GET /api/v1/campaigns/:campaign/history": async (route, state) => {
        await route.fulfill({ json: state.campaigns });
        return;
    },
    "GET /api/v1/campaigns/:campaign/progress": async (route, state) => {
        await route.fulfill({ json: { campaign_id: state.campaignId, version: state.campaigns[0].version, action: "ready",
                step_id: "step-1", reason: "All declared dependencies reached their required outcomes." } });
        return;
    }
};
