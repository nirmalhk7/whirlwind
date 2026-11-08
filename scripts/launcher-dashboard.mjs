import { spawn } from "node:child_process";
import { mkdtemp, realpath, rm } from "node:fs/promises";
import { homedir, tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

function foreground(command, args, options) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, { ...options, stdio: "inherit" });
    const interrupt = () => child.kill("SIGINT");
    const terminate = () => child.kill("SIGTERM");
    process.on("SIGINT", interrupt);
    process.on("SIGTERM", terminate);
    const cleanup = () => {
      process.off("SIGINT", interrupt);
      process.off("SIGTERM", terminate);
    };
    child.once("error", error => { cleanup(); reject(error); });
    child.once("close", (code, signal) => { cleanup(); resolve(code ?? (signal === "SIGINT" ? 130 : 1)); });
  });
}

export async function dashboard() {
  const root = await realpath(process.env.WHIRLWIND_ROOT || dirname(dirname(fileURLToPath(import.meta.url))));
  const data = resolve(root, process.env.WHIRLWIND_DATA_DIR || join(homedir(), ".local/share/whirlwind"));
  const temporary = await mkdtemp(join(tmpdir(), "whirlwind-dashboard-"));
  const binary = join(temporary, "dashboard");
  const options = { cwd: root, env: { ...process.env, WHIRLWIND_DATA_DIR: data } };
  try {
    const built = await foreground("go", ["build", "-o", binary, "./cmd/whirlwind-dashboard"], options);
    if (built !== 0) return built;
    return await foreground(binary, [], options);
  } finally { await rm(temporary, { recursive: true, force: true }); }
}
