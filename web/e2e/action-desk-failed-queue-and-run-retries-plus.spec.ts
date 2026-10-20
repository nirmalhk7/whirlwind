import { mockFailedControls } from "./fixtures/failed-controls";
import { expect, test, type Page, type Route } from "@playwright/test";
import { planFixture, mockDeskApi } from './fixtures/desk-api';
test("failed queue and run retries plus daemon controls are actionable on the Action Desk", async ({ page }) => {
    const { records, submitted } = await mockFailedControls(page);
    await page.goto("/");
    await expect(page.getByText("7 open items")).toBeVisible();
    const actions = [
        { id: "queue-failed", button: "Retry queue item", status: /Retry queued/ },
        { id: "run-failed", button: "Retry as new run", status: /New run queued/ },
        { id: "daemon-failed", button: "Start daemon", status: /Daemon started/ },
        { id: "daemon-running", button: "Restart daemon", status: /Daemon restarted/ },
        { id: "daemon-running", button: "Stop daemon", status: /Daemon stop requested/ },
    ];
    for (const action of actions) {
        await page.locator(".action-card").filter({ hasText: records.get(action.id).summary })
            .getByRole("button", { name: /Review evidence and act/ }).click();
        const dialog = page.getByRole("dialog");
        await expect(dialog.getByRole("button", { name: action.button })).toBeVisible();
        await dialog.getByRole("button", { name: action.button }).click();
        await expect(dialog.getByRole("status")).toContainText(action.status);
        await expect(page).toHaveURL(/\/$/);
        await dialog.getByRole("button", { name: "Close action details" }).click();
    }
    expect(submitted.map(({ path }) => path)).toEqual([
        "/api/v1/queue/queue-failed/requeues",
        "/api/v1/executions/run-failed/retries",
        "/api/v1/daemons/start",
        "/api/v1/daemons/daemon-running/restarts",
        "/api/v1/daemons/daemon-running/stops",
    ]);
    expect(submitted[0].body).toEqual({ expected_updated_at: "queue-rev-1" });
    expect(submitted[1].body).toEqual({ expected_updated_at: "run-rev-1" });
    expect(submitted[3].body).toEqual({ expected_updated_at: "daemon-rev-2" });
    const openAction = async (id: string) => {
        await page.locator(".action-card").filter({ hasText: records.get(id).summary })
            .getByRole("button", { name: /Review evidence and act/ }).click();
        return page.getByRole("dialog");
    };
    let dialog = await openAction("run-active");
    await dialog.getByRole("button", { name: "Pause provider" }).click();
    await expect(dialog.getByRole("status")).toContainText("Pause requested");
    await expect(dialog.getByRole("button", { name: "Resume provider" })).toBeVisible();
    await dialog.getByRole("button", { name: "Resume provider" }).click();
    await expect(dialog.getByRole("status")).toContainText("Resume requested");
    await dialog.getByRole("button", { name: "Cancel work" }).click();
    await expect(dialog.getByRole("status")).toContainText("Cancellation requested");
    await dialog.getByRole("button", { name: "Close action details" }).click();
    records.get("run-active").status = "reviewing";
    await page.reload();
    dialog = await openAction("run-active");
    await expect(dialog.getByRole("button", { name: "Cancel work" })).toBeVisible();
    await dialog.getByRole("button", { name: "Cancel work" }).click();
    await expect(dialog.getByRole("status")).toContainText("Cancellation requested");
    await dialog.getByRole("button", { name: "Close action details" }).click();
    dialog = await openAction("queue-active");
    await dialog.getByRole("button", { name: "Cancel work" }).click();
    await expect(dialog.getByRole("status")).toContainText("Cancellation requested");
    await dialog.getByRole("button", { name: "Close action details" }).click();
    dialog = await openAction("session-active");
    await dialog.getByLabel("Direction").fill("Continue after reviewing the retained evidence.");
    await dialog.getByRole("button", { name: "Queue steering" }).click();
    await expect(dialog.getByRole("status")).toContainText("Steering queued");
    await dialog.getByRole("button", { name: "Cancel work" }).click();
    await expect(dialog.getByRole("status")).toContainText("Cancellation requested");
    await expect(page).toHaveURL(/\/$/);
    expect(submitted.slice(5).map(({ path }) => path)).toEqual([
        "/api/v1/executions/run-active/controls",
        "/api/v1/executions/run-active/controls",
        "/api/v1/executions/run-active/cancellations",
        "/api/v1/executions/run-active/cancellations",
        "/api/v1/executions/queue-active/cancellations",
        "/api/v1/sessions/session-active/steers",
        "/api/v1/executions/session-active/cancellations",
    ]);
    expect(submitted[5].body).toEqual({ action: "pause" });
    expect(submitted[6].body).toEqual({ action: "resume" });
    expect(submitted[10].body).toEqual({
        message: "Continue after reviewing the retained evidence.", expected_updated_at: "session-active-rev-1",
    });
    expect(submitted.every(({ key }) => Boolean(key))).toBe(true);
});
