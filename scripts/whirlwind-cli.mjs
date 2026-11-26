import {
  startSupervisor,
  stopSupervisor,
  restartSupervisor,
  supervisorStatus,
} from "./local-supervisor.mjs";
import { dashboard } from "./launcher-dashboard.mjs";
import { logs } from "./launcher-logs.mjs";
import { localConfiguration, request } from "./local-supervisor.mjs";
import { execFile } from "node:child_process";
import { open } from "node:fs/promises";
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
  policy preview PROJECT FILE
  policy approve PROJECT FILE VERSION HASH
  memory list PROJECT ROLE
  memory remember PROJECT ROLE ATTEMPT TEXT
  memory clear PROJECT ROLE

The dashboard is available with ./bin/whirlwind dashboard.
The supervisor uses a private local socket; it starts no browser or UI server.
WHIRLWIND_NONO=off disables native execution for disposable testing. Default: on.
`;

export async function runCLI(args) {
  const helpRequested =
    !args.length || (args.length === 1 && ["--help", "-h", "help"].includes(args[0]));
  if (helpRequested) console.log(help);
  const policyRequested = args[0] === "policy";
  const memoryRequested = args[0] === "memory";
  const valid =
    policyRequested ||
    memoryRequested ||
    (args.length === 1 &&
      ["dashboard", "start", "status", "stop", "restart", "doctor", "logs"].includes(args[0])) ||
    (args.length === 2 && args[0] === "logs" && args[1] === "--follow");
  if (!helpRequested && !valid) console.error(help);
  if (helpRequested || !valid) return helpRequested ? 0 : 1;
  if (!["darwin", "linux"].includes(process.platform))
    throw new Error("Whirlwind launcher supports macOS and Linux.");
  if (policyRequested) return projectPolicy(args.slice(1));
  if (memoryRequested) return projectMemory(args.slice(1));
  const commands = {
    dashboard,
    start: startSupervisor,
    status: supervisorStatus,
    stop: stopSupervisor,
    restart: restartSupervisor,
    doctor,
    logs: () => logs({ follow: args[1] === "--follow" }),
  };
  return commands[args[0]]();
}

async function projectMemory(args) {
  const [action, project, role, attempt, note] = args;
  const remember = action === "remember" && args.length === 5 && attempt && note;
  if (
    !project ||
    !["ideate", "plan", "code", "review"].includes(role) ||
    (!remember && !(["list", "clear"].includes(action) && args.length === 3))
  ) {
    throw new Error(
      "Use memory list PROJECT ROLE, memory remember PROJECT ROLE ATTEMPT TEXT, or memory clear PROJECT ROLE",
    );
  }
  const config = await localConfiguration();
  const scope = { project_id: project, resource_id: role };
  const { result: memory } = await request(config, "inspect.memory", scope);
  let result = memory;
  if (action === "clear") {
    ({ result } = await request(config, "memory.clear", {
      ...scope,
      expected_version: memory.version,
    }));
  } else if (remember) {
    if (!memory.enabled)
      throw new Error(
        "Project memory requires an approved external policy with project_memory enabled",
      );
    ({ result } = await request(config, "memory.remember", {
      ...scope,
      expected_version: memory.version,
      content_hash: memory.policy_hash,
      payload: { attempt_id: attempt, note },
    }));
  }
  console.log(JSON.stringify(result, null, 2));
  return 0;
}

async function projectPolicy(args) {
  const [action, project, file, version, hash] = args;
  const preview = action === "preview" && args.length === 3;
  const approve =
    action === "approve" &&
    args.length === 5 &&
    /^[1-9]\d*$/.test(version) &&
    Number.isSafeInteger(Number(version)) &&
    /^[0-9a-f]{64}$/.test(hash);
  if ((!preview && !approve) || !project || !file)
    throw new Error("Use policy preview PROJECT FILE or policy approve PROJECT FILE VERSION HASH");
  const document = await open(file, "r");
  let content;
  try {
    if (!(await document.stat()).isFile()) throw new Error("Managed policy must be a regular file");
    const buffer = Buffer.alloc(64 * 1024 + 1);
    let length = 0;
    while (length < buffer.length) {
      const { bytesRead } = await document.read(buffer, length, buffer.length - length);
      if (bytesRead === 0) break;
      length += bytesRead;
    }
    if (length === buffer.length) throw new Error("Managed policy exceeds 64 KiB");
    content = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(
      buffer.subarray(0, length),
    );
  } finally {
    await document.close();
  }
  const { result } = await request(await localConfiguration(), `project.policy.${action}`, {
    project_id: project,
    expected_version: approve ? Number(version) : undefined,
    content_hash: approve ? hash : undefined,
    payload: { content },
  });
  if (preview) {
    console.log(
      `Project ${result.project_id}\nCheckout ${result.path}\nVersion ${result.version}\nSHA256 ${result.hash}\n\n${content}`,
    );
  } else {
    console.log(
      `Approved policy ${result.hash} for project ${result.project_id} at version ${result.version}.`,
    );
  }
  return 0;
}

async function doctor() {
  const config = await localConfiguration();
  console.log(
    `Checkout: ${config.root}\nData: ${config.data}\nSupervisor socket: ${config.socket}`,
  );
  try {
    console.log(
      (await exec("go", ["version"], { cwd: config.root, timeout: 15000 })).stdout.trim(),
    );
  } catch {
    console.error("FAIL Go toolchain unavailable; install the Go version required by go.mod.");
    return 1;
  }
  console.log("OK Local supervisor can start without browser or frontend dependencies.");
  return 0;
}
