import type { Page, Route } from '@playwright/test';
import { baseHandlers } from './desk-base-routes';
import { discoveryHandlers } from './desk-discovery-routes';
import { campaignHandlers } from './desk-campaign-routes';
import { planHandlers } from './desk-plan-routes';
type PlanContent = {
    objective: string;
    acceptance_criteria: string[];
    allowed_paths: string[];
    checks: string[];
    workflow: "single" | "pipeline";
    unresolved_requirements: string[];
};
export function planFixture(unresolved = ["What should count as complete?"]): {
    plan: any;
    submissions: any[];
    deferredPlans: any[];
} {
    const submissions: any[] = [];
    const deferredPlans: any[] = [];
    return {
        submissions,
        deferredPlans,
        plan: {
            id: "smoke-task",
            type: "plan",
            project: "smoke-project",
            status: "draft",
            summary: "Draft plan: make the Action Desk clearer",
            updated_at: "2026-10-11T00:00:00Z",
            record: {
                version: 1,
                content: {
                    objective: "Make the Action Desk clearer",
                    acceptance_criteria: ["All decisions stay on the Action Desk"],
                    allowed_paths: ["web/app/page.tsx"],
                    checks: [],
                    workflow: "pipeline",
                    unresolved_requirements: unresolved,
                } satisfies PlanContent,
            },
            evidence: { plan: { version: 1, content: { objective: "Make the Action Desk clearer" } } },
        },
    };
}
export type DeskState = {
    fixture: ReturnType<typeof planFixture>;
    editStatus: number;
    inboxItems?: any[];
    latestDiscoveryScan: any;
    proposals: any[];
    campaigns: any[];
    campaignId: string;
};
export type DeskHandlers = Record<string, (route: Route, state: DeskState) => Promise<void>>;
const handlers: DeskHandlers = { ...baseHandlers, ...discoveryHandlers, ...campaignHandlers, ...planHandlers };
export async function mockDeskApi(page: Page, fixture: ReturnType<typeof planFixture>, editStatus = 200, inboxItems?: any[]) {
    const state: DeskState = { fixture, editStatus, inboxItems, latestDiscoveryScan: null, proposals: [], campaigns: [], campaignId: '' };
    await page.route('**/api/v1/**', async (route) => {
        const request = route.request();
        const path = new URL(request.url()).pathname;
        const normalized = path.replace('/' + state.campaignId + '/', '/:campaign/');
        const handler = handlers[request.method() + ' ' + normalized] ?? handlers['* ' + normalized];
        if (handler) {
            await handler(route, state);
            return;
        }
        await route.fulfill({ status: 404, json: { error: { message: 'Unexpected smoke-test request: ' + path } } });
    });
}
