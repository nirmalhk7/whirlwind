import { defineConfig } from "@playwright/test";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { workspace } from "./e2e/fixtures/workspace";
const fixture = workspace();
const baseURL = "http://127.0.0.1:3107";
const backendURL = "http://127.0.0.1:8097";
export default defineConfig({
  testDir: "./e2e",
  testMatch: ["go-workflow.spec.ts", "control-security.spec.ts"],
  fullyParallel: false, retries: 0, reporter: "list",
  use: { baseURL, browserName: "chromium", launchOptions: process.env.KHAN_CHROMIUM_EXECUTABLE ? { executablePath: process.env.KHAN_CHROMIUM_EXECUTABLE } : undefined },
  webServer: [
    { command: `go run ../cmd/khan --data "${join(tmpdir(), `khan-e2e-${process.pid}`)}" --listen 127.0.0.1:8097 --claude-bridge ../bridges/claude/bridge.mjs`, url: `${backendURL}/api/v1/health`, env: { PATH: `${fixture.bin}:${process.env.PATH}`, GOCACHE: process.env.GOCACHE ?? join(tmpdir(), "khan-go-cache"), GOPATH: process.env.GOPATH ?? join(tmpdir(), "khan-go") }, reuseExistingServer: false, timeout: 120_000 },
    { command: "npm run dev -- --hostname 127.0.0.1 --port 3107", url: `${baseURL}/api/v1/health`, env: { KHAN_NEXT_DIST_DIR: ".next-playwright", KHAN_BACKEND_URL: backendURL }, reuseExistingServer: false, timeout: 120_000 },
  ],
});
