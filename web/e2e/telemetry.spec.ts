import { expect, test } from "@playwright/test";

test("agent detail separates qualified usage from local operational diagnostics", async ({ page }) => {
  await page.route("**/api/v1/agents/run-one", async (route) => route.fulfill({ json: {
    id: "run-one", type: "run", provider: "codex", project: "demo", status: "failed",
    summary: "Run stopped", events: [], invocations: [{ id: "inv-one", role: "worker", attempt: 2,
      status: "failed", requested_model: "model-a", effective_model: "model-a", completeness: "partial",
      output_bytes: 512, usage: { input_tokens: null, output_tokens: null, availability: ["unavailable"] },
      diagnostics: { provider_event_count: 5, tool_call_count: 2, tool_output_bytes: 320,
        retry_event_count: 1, invalid_payload_count: 0, retrieval_receipt_count: 1,
        retrieval_supplied_bytes: 128, retrieval_repeated_request_count: 1,
        failure_reason: "provider_exit", availability: "locally_observed" } }],
  } }));
  await page.goto("/agents/detail?id=run-one");
  await expect(page.getByRole("heading", { name: "codex timeline" })).toBeVisible();
  await page.getByText("Operational diagnostics").click();
  await expect(page.getByText(/"tool_call_count": 2/)).toBeVisible();
  await expect(page.getByText(/"availability": \[\s*"unavailable"/)).toBeVisible();
  await expect(page.getByText(/hidden calls, caches, or reasoning/)).toBeVisible();
});
