import { execFile } from "node:child_process";
import { once } from "node:events";
import { rm } from "node:fs/promises";
import net from "node:net";
import { setTimeout as delay } from "node:timers/promises";
import { promisify } from "node:util";
import { readState } from "./launcher-config.mjs";

const exec = promisify(execFile);
const tokenKey = "WHIRLWIND_LAUNCHER_TOKEN";

// A registration token inherited by every child prevents stale PIDs from targeting other apps.
// Keep ps output private: it includes the environment of other local processes.
export async function ownedProcesses(state) {
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

export function signal(pid, name) {
  try { process.kill(pid, name); }
  catch (error) { if (error.code !== "ESRCH") throw error; }
}

export async function checkPort(host, port) {
  const server = net.createServer();
  try {
    server.listen({ host, port, exclusive: true });
    await once(server, "listening");
  } catch (error) { throw new Error(`Port ${host}:${port} unavailable: ${error.code}`); }
  finally { if (server.listening) await new Promise((done) => server.close(done)); }
}

export async function health(url, backend = false) {
  try {
    const response = await fetch(`${url}${backend ? "/api/v1/health" : "/"}`, { signal: AbortSignal.timeout(2000), redirect: "manual" });
    if (backend) return response.ok && (await response.json()).status === "ok";
    await response.body?.cancel();
    return response.status >= 200 && response.status < 400 && await health(url, true);
  } catch { return false; }
}

export async function report(config, state) {
  const processes = await ownedProcesses(state);
  const running = processes.some(({ pid }) => pid === state?.pid);
  const [backend, frontend] = running ? await Promise.all([health(state.backendURL, true), health(state.webURL)]) : [false, false];
  console.log(`Whirlwind: ${running ? (backend && frontend ? "running" : "degraded") : (processes.length ? "orphaned" : "stopped")}`);
  console.log(`Backend: ${backend ? "healthy" : "unavailable"} ${state?.backendURL || config.backendURL}`);
  console.log(`Frontend: ${frontend ? "healthy" : "unavailable"} ${state?.webURL || config.webURL}`);
  console.log(`Checkout: ${state?.root || config.root}\nLogs: ${config.log}`);
  return running && backend && frontend ? 0 : 1;
}

export async function stop(config, quiet = false) {
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
