import { spawn } from "node:child_process";
import { execFile } from "node:child_process";
import { createConnection } from "node:net";
import { createHash, randomBytes } from "node:crypto";
import { mkdir, open, readFile, rename, rm, writeFile } from "node:fs/promises";
import { homedir, tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { readState } from "./launcher-config.mjs";
import { ownedProcesses, signal } from "./launcher-processes.mjs";
import { withLock } from "./launcher-lock.mjs";

const exec = promisify(execFile);
const wait = ms => new Promise(resolve => setTimeout(resolve, ms));
const supervisorToken = "WHIRLWIND_SUPERVISOR_TOKEN";

export async function localConfiguration() {
  const root = await realRoot();
  const data = resolve(root, process.env.WHIRLWIND_DATA_DIR || join(homedir(), ".local/share/whirlwind"));
  const settings = resolve(root, process.env.WHIRLWIND_SETTINGS_FILE || "settings.yaml");
  const owner = typeof process.getuid === "function" ? process.getuid() : "unknown";
  const key = createHash("sha256").update(data).digest("hex").slice(0, 16);
  return { root, data, settings, socket: join("/tmp", `whirlwind-${owner}`, `${key}.sock`), log: join(data, "whirlwind-supervisor.log"), state: join(data, "control", "supervisor.json"), commandDirectory: join(data, "launcher") };
}

async function realRoot() {
  const { realpath } = await import("node:fs/promises");
  return realpath(process.env.WHIRLWIND_ROOT || dirname(dirname(fileURLToPath(import.meta.url))));
}

function connect(path, timeout = 500) {
  return new Promise((resolve, reject) => {
    const socket = createConnection(path);
    const timer = setTimeout(() => { socket.destroy(); reject(new Error("Supervisor connection timed out")); }, timeout);
    socket.once("connect", () => { clearTimeout(timer); resolve(socket); });
    socket.once("error", error => { clearTimeout(timer); reject(error); });
  });
}

async function socketIsActive(path) {
  try {
    const socket = await connect(path, 500);
    socket.destroy();
    return true;
  } catch (error) {
    if (["ECONNREFUSED", "ENOENT", "ETIMEDOUT"].includes(error.code)) return false;
    throw error;
  }
}

async function request(config, operation) {
  const socket = await connect(config.socket, 1500);
  try {
    const request = { request_id: randomBytes(12).toString("hex"), operation, event_cursor: 0, limit: 1 };
    if (!operation.startsWith("inspect.")) request.idempotency_key = randomBytes(24).toString("hex");
    const message = JSON.stringify(request);
    return await new Promise((resolve, reject) => {
      let output = "";
      socket.setTimeout(3000, () => reject(new Error("Supervisor response timed out")));
      socket.on("data", chunk => {
        output += chunk;
        if (output.length > 1_048_576) { reject(new Error("Supervisor response exceeded 1 MiB")); return; }
        if (!output.includes("\n")) return;
        try {
          const response = JSON.parse(output);
          if (response.request_id !== request.request_id) throw new Error("Supervisor response request_id mismatch");
          if (response.error) reject(new Error(response.error));
          else resolve(response);
        } catch (error) { reject(error); }
      });
      socket.once("error", reject);
      socket.end(message + "\n");
    });
  } finally { socket.destroy(); }
}

async function legacyInstance(config) {
  const state = await readState({ stateFile: join(config.data, "launcher", "state.json") });
  return Boolean(state && (await ownedProcesses(state)).length);
}

async function buildSupervisor(config) {
  const runtime = join(config.data, "runtime");
  await mkdir(runtime, { recursive: true, mode: 0o700 });
  const temporary = join(runtime, `.whirlwind-supervisor-${process.pid}`);
  const binary = join(runtime, "whirlwind-supervisor");
  try {
    await exec("go", ["build", "-o", temporary, "./cmd/whirlwind"], { cwd: config.root, timeout: 300_000, maxBuffer: 4 * 1024 * 1024 });
    await rename(temporary, binary);
  } catch (error) {
    await recordBuildFailure(config, error);
    throw new Error(`Supervisor build failed. Logs: ${config.log}`);
  } finally { await rm(temporary, { force: true }); }
  return binary;
}

async function recordBuildFailure(config, error) {
  await mkdir(dirname(config.log), { recursive: true, mode: 0o700 });
  const log = await open(config.log, "a", 0o600);
  try {
    await log.chmod(0o600);
    await log.writeFile(`${new Date().toISOString()} Supervisor build failed\n${error.stderr || error.stdout || error.message}\n`);
    await log.sync();
  } finally { await log.close(); }
}

export async function startSupervisor() {
  const config = await localConfiguration();
  return withLock({ directory: config.commandDirectory }, () => startLocked(config));
}

async function startLocked(config) {
  if (await isHealthy(config)) { console.log("Whirlwind supervisor is already running."); return 0; }
  if (await socketIsActive(config.socket)) throw new Error(`Control socket ${config.socket} is active but did not return a matching Whirlwind supervisor response; refusing to replace it.`);
  if (await legacyInstance(config)) throw new Error("A legacy HTTP/browser instance owns this data directory. Run `./bin/whirlwind stop` to stop its owned processes, then run `./bin/whirlwind start` again.");
  const orphan = await readSupervisorState(config);
  if (orphan && (await ownedProcesses(orphan, supervisorToken)).length) {
    throw new Error("A Whirlwind supervisor process still owns this state directory but its socket is unavailable. Run `./bin/whirlwind stop` before starting it again.");
  }
  const binary = await buildSupervisor(config);
  await mkdir(dirname(config.log), { recursive: true, mode: 0o700 });
  const log = await open(config.log, "a", 0o600);
  const state = { version: 1, token: randomBytes(16).toString("hex"), pid: 0, root: config.root, startedAt: new Date().toISOString() };
  try {
    const child = spawn(binary, ["--supervisor", "--data", config.data, "--settings", config.settings], {
      cwd: config.root, env: { ...process.env, WHIRLWIND_DATA_DIR: config.data, WHIRLWIND_SETTINGS_FILE: config.settings, [supervisorToken]: state.token },
      detached: true, stdio: ["ignore", log.fd, log.fd],
    });
    await new Promise((resolve, reject) => {
      child.once("spawn", resolve);
      child.once("error", reject);
    });
    state.pid = child.pid;
    await saveSupervisorState(config, state);
    child.unref();
  } catch (error) { await log.close(); throw error; }
  await log.close();
  const deadline = Date.now() + 30_000;
  let lastError;
  while (Date.now() < deadline) {
    try { await request(config, "inspect.events"); console.log(`Whirlwind supervisor started. State: ${config.data}`); return 0; }
    catch (error) { lastError = error; await wait(150); }
  }
  throw new Error(`Supervisor did not become ready: ${lastError?.message || "startup timed out"}. Logs: ${config.log}`);
}

async function readSupervisorState(config) {
  try {
    const state = JSON.parse(await readFile(config.state, "utf8"));
    if (state.version !== 1 || !/^[a-f0-9]{32}$/.test(state.token) || !Number.isInteger(state.pid) || state.pid < 1) return null;
    return state;
  } catch (error) { if (error.code === "ENOENT") return null; throw new Error(`Cannot read ${config.state}: ${error.message}`); }
}

async function saveSupervisorState(config, state) {
  await mkdir(dirname(config.state), { recursive: true, mode: 0o700 });
  const temporary = `${config.state}.${process.pid}`;
  try { await writeFile(temporary, JSON.stringify(state) + "\n", { mode: 0o600 }); await rename(temporary, config.state); }
  finally { await rm(temporary, { force: true }); }
}

export async function isHealthy(config) {
  try { const result = await request(config, "inspect.events"); return Array.isArray(result.result); }
  catch { return false; }
}

export async function supervisorStatus() {
  const config = await localConfiguration();
  if (await isHealthy(config)) { console.log(`Whirlwind supervisor: running\nState: ${config.data}\nLogs: ${config.log}`); return 0; }
  const state = await readSupervisorState(config);
  const processes = await ownedProcesses(state, supervisorToken);
  console.log(`Whirlwind supervisor: ${processes.length ? "orphaned; socket unavailable" : "stopped"}\nState: ${config.data}`);
  return 1;
}

export async function stopSupervisor() {
  const config = await localConfiguration();
  return withLock({ directory: config.commandDirectory }, () => stopLocked(config));
}

async function stopLocked(config) {
  if (await isHealthy(config)) {
    try { await request(config, "supervisor.stop"); } catch (error) { if (!/connect|socket|closed/i.test(error.message)) throw error; }
    const deadline = Date.now() + 15_000;
    while (Date.now() < deadline && await isHealthy(config)) await wait(100);
    if (await isHealthy(config)) throw new Error(`Supervisor did not stop. Inspect ${config.log}`);
    await rm(config.state, { force: true });
    console.log("Whirlwind supervisor stopped.");
    return 0;
  }
  const legacyState = await readState({ stateFile: join(config.data, "launcher", "state.json") });
  if (legacyState && (await ownedProcesses(legacyState)).length) {
    const { stop } = await import("./launcher-processes.mjs");
    return stop({ ...config, stateFile: join(config.data, "launcher", "state.json"), log: join(config.data, "launcher", "whirlwind.log") });
  }
  const state = await readSupervisorState(config);
  let processes = await ownedProcesses(state, supervisorToken);
  if (processes.length) {
    signal(state.pid, "SIGTERM");
    const deadline = Date.now() + 12_000;
    while (processes.length && Date.now() < deadline) { await wait(100); processes = await ownedProcesses(state, supervisorToken); }
    for (const group of new Set(processes.map(({ group }) => group))) signal(-group, "SIGKILL");
    for (let attempt = 0; processes.length && attempt < 30; attempt++) { await wait(100); processes = await ownedProcesses(state, supervisorToken); }
    if (processes.length) throw new Error(`Managed supervisor remains active. Inspect ${config.log}`);
  }
  await rm(config.state, { force: true });
  console.log("Whirlwind supervisor already stopped.");
  return 0;
}

export async function restartSupervisor() {
  const config = await localConfiguration();
  return withLock({ directory: config.commandDirectory }, async () => { await stopLocked(config); return startLocked(config); });
}
