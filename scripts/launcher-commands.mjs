import { startSupervisor, supervisorStatus } from "./local-supervisor.mjs";

export async function start() { return startSupervisor(); }

export async function doctor() {
  const result = await startSupervisor();
  if (result === 0) console.log("Supervisor prerequisites are ready.");
  return result;
}

export async function restart() {
  throw new Error("Use `./bin/whirlwind restart` to restart the local supervisor.");
}

export { supervisorStatus as status };
