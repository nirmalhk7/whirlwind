import { expect, test } from "@playwright/test";

test("control API rejects a non-loopback Host before routing", async ({ request }) => {
  const response = await request.get("/api/v1/health", {
    headers: { host: "attacker.example" },
  });

  expect(response.status()).toBe(403);
  expect(await response.json()).toMatchObject({ error: { code: "local_host_required" } });
});

test("control API rejects an Origin that does not match its loopback Host", async ({ request }) => {
  const response = await request.get("/api/v1/health", {
    headers: {
      host: "127.0.0.1:3107",
      origin: "http://attacker.example",
    },
  });

  expect(response.status()).toBe(403);
  expect(await response.json()).toMatchObject({ error: { code: "origin_rejected" } });
});
