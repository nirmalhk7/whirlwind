import { execFile, spawn } from "node:child_process";
import { randomBytes } from "node:crypto";
import { once } from "node:events";
import { access, mkdir, open, readFile, realpath, rename, rm, stat, writeFile } from "node:fs/promises";
import net from "node:net";
import { homedir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";

const exec = promisify(execFile);
const tokenKey = "WHIRLWIND_LAUNCHER_TOKEN";
const help = `Usage: whirlwind <command>

  start    Build backend; start backend and frontend in background
  status   Show process health, URLs, checkout, and log path
  stop     Stop managed backend, frontend, and descendants
  restart  Stop, rebuild, and start both services
  doctor   Check runtime, dependencies, ports, and service health

Add this checkout's bin directory to PATH, or symlink bin/whirlwind.
Defaults: UI http://127.0.0.1:9876, backend http://127.0.0.1:9877.
Overrides: WHIRLWIND_ROOT, WHIRLWIND_DATA_DIR, WHIRLWIND_LISTEN,
           WHIRLWIND_WEB_PORT, WHIRLWIND_SETTINGS_FILE.
`;

async function configuration() {
  const root = await realpath(process.env.WHIRLWIND_ROOT || dirname(dirname(fileURLToPath(import.meta.url))));
  const data = resolve(root, process.env.WHIRLWIND_DATA_DIR || join(homedir(), ".local/share/whirlwind"));
  const listen = process.env.WHIRLWIND_LISTEN || "127.0.0.1:9877";
  const match = /^(127\.0\.0\.1|localhost|\[::1\]):(\d+)$/.exec(listen);
  const webPort = process.env.WHIRLWIND_WEB_PORT || "9876";
  const validPort = (value) => /^\d+$/.test(value) && Number(value) > 0 && Number(value) <= 65535;
  if (!match || !validPort(match[2])) throw new Error("WHIRLWIND_LISTEN must be a loopback host:port (1-65535).");
  if (!validPort(webPort)) throw new Error("WHIRLWIND_WEB_PORT must be a port (1-65535).");
  if (Number(match[2]) === Number(webPort)) throw new Error("Backend and frontend ports must differ.");
  const directory = join(data, "launcher");
  return {
    root, data, listen, webPort, backendURL: `http://${listen}`, webURL: `http://127.0.0.1:${webPort}`,
    host: match[1].replace(/[\[\]]/g, ""), backendPort: Number(match[2]),
    directory, stateFile: join(directory, "state.json"), log: join(directory, "whirlwind.log"),
    env: { ...process.env, WHIRLWIND_DATA_DIR: data,
      WHIRLWIND_SETTINGS_FILE: resolve(root, process.env.WHIRLWIND_SETTINGS_FILE || "settings.yaml"),
      WHIRLWIND_LISTEN: listen, WHIRLWIND_WEB_PORT: webPort,
      WHIRLWIND_BACKEND_URL: `http://${listen}` },
  };
}

async function exists(path) {
  try { await access(path); return true; }
  catch (error) { if (error.code === "ENOENT") return false; throw error; }
}

async function readState(config) {
  try {
    const state = JSON.parse(await readFile(config.stateFile, "utf8"));
    if (state.version !== 1 || !/^[a-f0-9]{32}$/.test(state.token) || !Number.isInteger(state.pid) || state.pid < 0) {
      throw new Error("invalid process registration");
    }
    return state;
  } catch (error) {
    if (error.code === "ENOENT") return null;
    throw new Error(`Cannot read ${config.stateFile}: ${error.message}`);
  }
}

async function saveState(config, state) {
  const temporary = `${config.stateFile}.${process.pid}`;
  try {
    await writeFile(temporary, JSON.stringify(state, null, 2) + "\n", { mode: 0o600 });
    await rename(temporary, config.stateFile);
  } finally { await rm(temporary, { force: true }); }
}

// A registration token inherited by every child prevents stale PIDs from targeting other apps.
// Keep ps output private: it includes the environment of other local processes.
async function ownedProcesses(state) {
  if (!state) return [];
  const { stdout } = await exec("ps", ["eww", "-A", "-o", "pid=", "-o", "pgid=", "-o", "stat=", "-o", "command="], { maxBuffer: 64 * 1024 * 1024 });
  const processes = [];
  for (const line of stdout.split("\n")) {
    const match = /^\s*(\d+)\s+(\d+)\s+(\S+)\s+(.*)$/.exec(line);
    if (match && !match[3].startsWith("Z") && match[4].split(/\s+/).includes(`${tokenKey}=${state.token}`)) {
      const pid = Number(match[1]), group = Number(match[2]);
      if (pid > 1 && group > 1) processes.push({ pid, group });
    }
  }
  return processes;
}

function signal(pid, name) {
  try { process.kill(pid, name); }
  catch (error) { if (error.code !== "ESRCH") throw error; }
}

function alive(pid) {
  try { process.kill(pid, 0); return true; }
  catch (error) { if (error.code === "ESRCH") return false; throw error; }
}

async function withLock(config, action) {
  await mkdir(config.directory, { recursive: true, mode: 0o700 });
  const path = join(config.directory, "command.lock");
  const deadline = Date.now() + 150_000;
  let lock;
  while (!lock) {
    try {
      lock = await open(path, "wx", 0o600);
      await lock.writeFile(JSON.stringify({ pid: process.pid }));
    } catch (error) {
      if (error.code !== "EEXIST") throw error;
      try {
        const text = await readFile(path, "utf8");
        // Allow the creator to finish writing before treating an empty lock as stale.
        const owner = text ? JSON.parse(text).pid : null;
        if ((Number.isInteger(owner) && owner > 1 && !alive(owner)) || (!owner && Date.now() - (await stat(path)).mtimeMs > 2000)) {
          await rm(path, { force: true });
          continue;
        }
      } catch (error) { if (error.code !== "ENOENT") throw error; }
      if (Date.now() >= deadline) throw new Error(`Another whirlwind command is busy (${path}).`);
      await delay(100);
    }
  }
  try { return await action(); }
  finally { await lock.close(); await rm(path, { force: true }); }
}

async function checkPort(host, port) {
  const server = net.createServer();
  try {
    server.listen({ host, port, exclusive: true });
    await once(server, "listening");
  } catch (error) { throw new Error(`Port ${host}:${port} unavailable: ${error.code}`); }
  finally { if (server.listening) await new Promise((done) => server.close(done)); }
}

async function health(url, backend = false) {
  try {
    const response = await fetch(`${url}${backend ? "/api/v1/health" : "/"}`, { signal: AbortSignal.timeout(2000), redirect: "manual" });
    if (backend) return response.ok && (await response.json()).status === "ok";
    await response.body?.cancel();
    return response.status >= 200 && response.status < 400 && await health(url, true);
  } catch { return false; }
}

async function report(config, state) {
  const processes = await ownedProcesses(state);
  const running = processes.some(({ pid }) => pid === state?.pid);
  const [backend, frontend] = running ? await Promise.all([health(state.backendURL, true), health(state.webURL)]) : [false, false];
  console.log(`Whirlwind: ${running ? (backend && frontend ? "running" : "degraded") : (processes.length ? "orphaned" : "stopped")}`);
  console.log(`Backend: ${backend ? "healthy" : "unavailable"} ${state?.backendURL || config.backendURL}`);
  console.log(`Frontend: ${frontend ? "healthy" : "unavailable"} ${state?.webURL || config.webURL}`);
  console.log(`Checkout: ${state?.root || config.root}\nLogs: ${config.log}`);
  return running && backend && frontend ? 0 : 1;
}

async function stop(config, quiet = false) {
  const state = await readState(config);
  let processes = await ownedProcesses(state);
  if (processes.some(({ pid }) => pid === state.pid)) signal(state.pid, "SIGTERM");
  else for (const group of new Set(processes.map(({ group }) => group))) signal(-group, "SIGTERM");
  const deadline = Date.now() + 12_000;
  while (processes.length && Date.now() < deadline) {
    await delay(100);
    processes = await ownedProcesses(state);
  }
  // Recover descendants even when their supervisor was killed before graceful cleanup.
  for (const group of new Set(processes.map(({ group }) => group))) signal(-group, "SIGKILL");
  for (let attempt = 0; processes.length && attempt < 30; attempt++) {
    await delay(100);
    processes = await ownedProcesses(state);
  }
  if (processes.length) throw new Error(`Managed processes still running. Inspect ${config.log}`);
  await rm(config.stateFile, { force: true });
  if (!quiet) console.log("Whirlwind stopped (backend and frontend).");
  return 0;
}

async function prerequisites(config) {
  const module = await readFile(join(config.root, "go.mod"), "utf8");
  if (!/^module whirlwind\s*$/m.test(module)) throw new Error(`Not a Whirlwind checkout: ${config.root}`);
  if (!await exists(join(config.root, "scripts/dev.mjs"))) throw new Error("Missing scripts/dev.mjs.");
  if (!await exists(join(config.root, "web/node_modules/next/dist/bin/next"))) throw new Error("Install frontend dependencies first: npm ci --prefix web");
  if (!versionAtLeast(process.versions.node, "20.9.0")) throw new Error("Node.js 20.9+ required.");
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

async function start(config) {
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
  // The existing dev supervisor stops the other service whenever either service exits.
  const log = await open(config.log, "a", 0o600);
  try {
    await log.write(`\n[${state.startedAt}] Starting Whirlwind from ${config.root}\n`);
    const child = spawn(process.execPath, [join(config.root, "scripts/dev.mjs")], {
      cwd: config.root, env: { ...config.env, [tokenKey]: state.token }, detached: true,
      stdio: ["ignore", log.fd, log.fd],
    });
    await once(child, "spawn");
    child.unref();
    state.pid = child.pid;
    await saveState(config, state);
  } catch (error) { await stop(config, true); throw error; }
  finally { await log.close(); }
  console.log("Starting Whirlwind (building backend and waiting for both services)...");
  try {
    const deadline = Date.now() + 120_000;
    while (Date.now() < deadline) {
      const processes = await ownedProcesses(state);
      if (!processes.some(({ pid }) => pid === state.pid)) throw new Error("Startup process exited");
      const [backend, frontend] = await Promise.all([health(config.backendURL, true), health(config.webURL)]);
      if (backend && frontend) return await report(config, state);
      await delay(250);
    }
    throw new Error("Startup timed out after 120 seconds");
  } catch (error) {
    await stop(config, true);
    throw new Error(`${error.message}. Logs: ${config.log}`);
  }
}

async function doctor(config) {
  let failures = 0;
  const check = async (name, action, optional = false) => {
    try { console.log(`OK   ${name}: ${await action() || "ready"}`); }
    catch (error) { console.log(`${optional ? "WARN" : "FAIL"} ${name}: ${error.message}`); if (!optional) failures++; }
  };
  console.log(`Checkout: ${config.root}\nData: ${config.data}\nLogs: ${config.log}`);
  await check("Node.js", async () => { if (!versionAtLeast(process.versions.node, "20.9.0")) throw new Error("Node.js 20.9+ required"); return process.version; });
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

export async function runCLI(args) {
  if (!args.length || (args.length === 1 && ["--help", "-h", "help"].includes(args[0]))) { console.log(help); return 0; }
  if (args.length !== 1 || !["start", "status", "stop", "restart", "doctor"].includes(args[0])) {
    console.error(help); return 1;
  }
  if (!["darwin", "linux"].includes(process.platform)) throw new Error("Whirlwind launcher supports macOS and Linux.");
  const config = await configuration();
  switch (args[0]) {
    case "status": return report(config, await readState(config));
    case "doctor": return doctor(config);
    case "start": return withLock(config, () => start(config));
    case "stop": return withLock(config, () => stop(config));
    case "restart": return withLock(config, async () => { await stop(config, true); return start(config); });
  }
}
