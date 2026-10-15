import { spawn } from "node:child_process";
import { existsSync } from "node:fs";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const next = join(root, "web/node_modules/next/dist/bin/next");
if (!existsSync(next)) {
  console.error("Install frontend dependencies first: npm ci --prefix web");
  process.exit(1);
}

const temporary = await mkdtemp(join(tmpdir(), "whirlwind-dev-"));
const binary = join(temporary, "whirlwind");
const listen = process.env.WHIRLWIND_LISTEN || "127.0.0.1:9877";
const port = process.env.WHIRLWIND_WEB_PORT || "9876";
const jobs = [];
let stopping = false;
let status = 0;
let forceStop;

function signalJobs(signal) {
  for (const { child } of jobs) {
    if (!child.pid) continue;
    try {
      if (process.platform === "win32") child.kill(signal);
      else process.kill(-child.pid, signal);
    } catch (error) {
      if (error.code !== "ESRCH") console.error(error.message);
    }
  }
}

function stop(code) {
  if (stopping) return;
  stopping = true;
  status = code;
  signalJobs("SIGTERM");
  forceStop = setTimeout(() => signalJobs("SIGKILL"), 5000);
}

function launch(command, args, cwd, env = process.env) {
  const child = spawn(command, args, { cwd, env, stdio: "inherit", detached: process.platform !== "win32" });
  const done = new Promise((resolve) => {
    child.on("error", (error) => { console.error(error.message); stop(1); });
    child.on("close", (code, signal) => resolve(code ?? (signal ? 1 : 0)));
  });
  jobs.push({ child, done });
  return done;
}

process.on("SIGINT", () => stop(0));
process.on("SIGTERM", () => stop(0));

try {
  const built = await launch("go", ["build", "-o", binary, "./cmd/whirlwind"], root);
  if (built !== 0) stop(built);
  if (!stopping) {
    console.log(`Whirlwind backend: http://${listen}\nWhirlwind UI: http://127.0.0.1:${port}\nCtrl-C stops both.`);
    const backend = launch(binary, ["--listen", listen], root);
    const frontend = launch(process.execPath, [next, "dev", "--hostname", "127.0.0.1", "--port", port], join(root, "web"), {
      ...process.env,
      WHIRLWIND_BACKEND_URL: process.env.WHIRLWIND_BACKEND_URL || `http://${listen}`,
    });
    stop(await Promise.race([backend, frontend]));
  }
  await Promise.all(jobs.map(({ done }) => done));
} finally {
  clearTimeout(forceStop);
  await rm(temporary, { recursive: true, force: true });
}
process.exitCode = status;
