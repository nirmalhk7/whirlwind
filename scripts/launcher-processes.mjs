import { execFile } from "node:child_process";
import { rm } from "node:fs/promises";
import { setTimeout as delay } from "node:timers/promises";
import { promisify } from "node:util";
import { readState } from "./launcher-config.mjs";

const exec = promisify(execFile);
const tokenKey = "WHIRLWIND_LAUNCHER_TOKEN";

// A registration token inherited by every child prevents stale PIDs from targeting other apps.
// Keep ps output private: it includes the environment of other local processes.
export async function ownedProcesses(state, tokenName = tokenKey) {
  if (!state) return [];
  const { stdout } = await exec("ps", ["eww", "-A", "-o", "pid=", "-o", "pgid=", "-o", "stat=", "-o", "command="], { maxBuffer: 64 * 1024 * 1024 });
  const processes = [];
  for (const line of stdout.split("\n")) {
    const match = /^\s*(\d+)\s+(\d+)\s+(\S+)\s+(.*)$/.exec(line);
    if (match && !match[3].startsWith("Z") && match[4].split(/\s+/).includes(`${tokenName}=${state.token}`)) {
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

export async function stop(config) {
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
  console.log("Legacy Whirlwind processes stopped.");
  return 0;
}
