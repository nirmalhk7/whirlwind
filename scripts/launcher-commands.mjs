import { spawn } from "node:child_process";
import { randomBytes } from "node:crypto";
import { once } from "node:events";
import { mkdir, open, readFile, rm } from "node:fs/promises";
import { join } from "node:path";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { setTimeout as delay } from "node:timers/promises";
import { exists, readState, saveState } from "./launcher-config.mjs";
import { checkPort, health, ownedProcesses, report, stop } from "./launcher-processes.mjs";
import { requireSandbox } from './sandbox-prerequisites.mjs';

const exec = promisify(execFile);
const tokenKey = "WHIRLWIND_LAUNCHER_TOKEN";

async function prerequisites(config) {
  const module = await readFile(join(config.root, "go.mod"), "utf8");
  if (!/^module whirlwind\s*$/m.test(module)) throw new Error(`Not a Whirlwind checkout: ${config.root}`);
  if (!await exists(join(config.root, "scripts/dev.mjs"))) throw new Error("Missing scripts/dev.mjs.");
  if (!await exists(join(config.root, "web/node_modules/next/dist/bin/next"))) throw new Error("Install frontend dependencies first: npm ci --prefix web");
  await requireSandbox(config.root);
  if (!versionAtLeast(process.versions.node, "22.12.0")) throw new Error("Node.js 22.12+ required.");
  let version;
  try { version = (await exec("go", ["version"], { cwd: config.root, timeout: 15_000 })).stdout.trim(); }
  catch { throw new Error("Go unavailable. Install the Go version required by go.mod."); }
  const required = /^go (\S+)$/m.exec(module)?.[1];
  const actual = /\bgo(\d+\.\d+(?:\.\d+)?)/.exec(version)?.[1];
  if (!actual || !required || !versionAtLeast(actual, required)) throw new Error(`Go ${required} or newer required; found ${version}.`);
  return version;
}

function versionAtLeast(actual, required) {
  const parts = actual.split(".").map(Number), minimum = required.split(".").map(Number);
  for (let i = 0; i < 3; i++) {
    if ((parts[i] || 0) !== (minimum[i] || 0)) return (parts[i] || 0) > (minimum[i] || 0);
  }
  return true;
}

export async function start(config) {
  let state = await readState(config);
  const processes = await ownedProcesses(state);
  if (processes.length) {
    if (state.root !== config.root) throw new Error(`Another checkout uses this data directory: ${state.root}. Run whirlwind stop first.`);
    if (processes.some(({ pid }) => pid === state.pid) && await report(config, state) === 0) return 0;
    throw new Error("Existing instance is unhealthy or orphaned. Run whirlwind restart.");
  }
  await prerequisites(config);
  await checkPort(config.host, config.backendPort);
  await checkPort("127.0.0.1", Number(config.webPort));
  state = { version: 1, token: randomBytes(16).toString("hex"), pid: 0, root: config.root,
    backendURL: config.backendURL, webURL: config.webURL, startedAt: new Date().toISOString() };
  await saveState(config, state);
  // Reuse the supervisor's shutdown handling with compiled production serving.
  const log = await open(config.log, "a", 0o600);
  try {
    await log.write(`\n[${state.startedAt}] Starting Whirlwind from ${config.root}\n`);
    const child = spawn(process.execPath, [join(config.root, "scripts/dev.mjs"), "--production"], {
      cwd: config.root, env: { ...config.env, [tokenKey]: state.token }, detached: true,
      stdio: ["ignore", log.fd, log.fd],
    });
    await once(child, "spawn");
    child.unref();
    state.pid = child.pid;
    await saveState(config, state);
  } catch (error) { await stop(config, true); throw error; }
  finally { await log.close(); }
  console.log("Starting Whirlwind (building backend and production UI; waiting for both services)...");
  try {
    const deadline = Date.now() + 300_000;
    while (Date.now() < deadline) {
      const processes = await ownedProcesses(state);
      if (!processes.some(({ pid }) => pid === state.pid)) throw new Error("Startup process exited");
      const [backend, frontend] = await Promise.all([health(config.backendURL, true), health(config.webURL)]);
      if (backend && frontend) return await report(config, state);
      await delay(250);
    }
    throw new Error("Startup timed out after 300 seconds");
  } catch (error) {
    await stop(config, true);
    throw new Error(`${error.message}. Logs: ${config.log}`);
  }
}

export async function doctor(config) {
  let failures = 0;
  const check = async (name, action, optional = false) => {
    try { console.log(`OK   ${name}: ${await action() || "ready"}`); }
    catch (error) { console.log(`${optional ? "WARN" : "FAIL"} ${name}: ${error.message}`); if (!optional) failures++; }
  };
  console.log(`Checkout: ${config.root}\nData: ${config.data}\nLogs: ${config.log}`);
  await check("Node.js", async () => { if (!versionAtLeast(process.versions.node, "22.12.0")) throw new Error("Node.js 22.12+ required"); return process.version; });
  await check("Go / frontend dependencies", () => prerequisites(config));
  await check("Process inspection", async () => { await exec("ps", ["-p", String(process.pid), "-o", "pid="]); });
  await check("State directory", async () => {
    await mkdir(config.directory, { recursive: true, mode: 0o700 });
    const path = join(config.directory, `.doctor-${process.pid}`);
    const file = await open(path, "wx", 0o600);
    await file.close(); await rm(path);
    return "writable";
  });
  let state;
  await check("Process registration", async () => { state = await readState(config); return state ? "present" : "stopped"; });
  if (state && (await ownedProcesses(state)).length) {
    await check("Running services", async () => { if (await report(config, state)) throw new Error("Run whirlwind restart; inspect logs."); });
  } else {
    await check("Backend port", () => checkPort(config.host, config.backendPort));
    await check("Frontend port", () => checkPort("127.0.0.1", Number(config.webPort)));
  }
  await check("Git", async () => (await exec("git", ["--version"], { timeout: 5000 })).stdout.trim(), true);
  await check("GitHub authentication", async () => {
    try { await exec("gh", ["auth", "status"], { timeout: 10_000 }); }
    catch { throw new Error("Install GitHub CLI and run gh auth login for GitHub workflows."); }
  }, true);
  return failures ? 1 : 0;
}
