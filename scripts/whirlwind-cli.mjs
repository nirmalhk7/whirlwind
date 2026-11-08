import { startSupervisor, stopSupervisor, restartSupervisor, supervisorStatus } from "./local-supervisor.mjs";
import { dashboard } from "./launcher-dashboard.mjs";
import { logs } from "./launcher-logs.mjs";
import { localConfiguration } from "./local-supervisor.mjs";
import { execFile } from "node:child_process";
import { promisify } from "node:util";

const exec = promisify(execFile);
const help = `Usage: whirlwind <command>

  dashboard Open the local terminal dashboard
  start     Start the durable local supervisor
  status    Show supervisor health and state path
  stop      Stop the local supervisor
  restart   Restart the local supervisor
  doctor    Check local supervisor prerequisites
  logs [--follow] Print all logs; optionally follow new output

The dashboard is available with ./bin/whirlwind dashboard.
The supervisor uses a private local socket; it starts no browser or UI server.
`;

export async function runCLI(args) {
  const helpRequested = !args.length || (args.length === 1 && ["--help", "-h", "help"].includes(args[0]));
  if (helpRequested) console.log(help);
  const valid = (args.length === 1 && ["dashboard", "start", "status", "stop", "restart", "doctor", "logs"].includes(args[0])) || (args.length === 2 && args[0] === "logs" && args[1] === "--follow");
  if (!helpRequested && !valid) console.error(help);
  if (helpRequested || !valid) return helpRequested ? 0 : 1;
  if (!["darwin", "linux"].includes(process.platform)) throw new Error("Whirlwind launcher supports macOS and Linux.");
  const commands = { dashboard, start: startSupervisor, status: supervisorStatus, stop: stopSupervisor, restart: restartSupervisor, doctor, logs: () => logs({ follow: args[1] === "--follow" }) };
  return commands[args[0]]();
}

async function doctor() {
  const config = await localConfiguration();
  console.log(`Checkout: ${config.root}\nData: ${config.data}\nSupervisor socket: ${config.socket}`);
  try { console.log((await exec("go", ["version"], { cwd: config.root, timeout: 15000 })).stdout.trim()); }
  catch { console.error("FAIL Go toolchain unavailable; install the Go version required by go.mod."); return 1; }
  console.log("OK Local supervisor can start without browser or frontend dependencies.");
  return 0;
}
