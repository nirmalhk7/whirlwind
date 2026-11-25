import { spawnSync } from "node:child_process";
import { readFileSync, realpathSync, writeFileSync } from "node:fs";
import { delimiter, dirname, isAbsolute, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const launcher = fileURLToPath(new URL("./launch.mjs", import.meta.url));
const commandRunner = fileURLToPath(new URL("./run-hook-command.mjs", import.meta.url));
const spec = process.argv[2];
const { hook, verdict, cwd, nono, readRoots } = JSON.parse(readFileSync(spec, "utf8"));

function executable(name) {
  if (!name.includes("/"))
    throw new Error("Hook executable must be resolved by the sandbox runtime");
  const target = realpathSync(resolve(cwd, name));
  const path = relative(cwd, target);
  if (!isAbsolute(name) && (path === ".." || path.startsWith("../") || isAbsolute(path))) {
    throw new Error("Relative hook executable escapes its snapshot");
  }
  return target;
}

function runChecks() {
  const output = [];
  const passed = new Set();
  for (const check of hook.checks) {
    const argv = [executable(check.command[0]), ...check.command.slice(1)];
    const missing = (check.depends_on ?? []).filter((name) => !passed.has(name));
    if (missing.length) {
      output.push(check.name + ": prerequisite failed (" + missing.join(", ") + ")\n");
      continue;
    }
    const profile = {
      cwd,
      read_only: true,
      read_roots: [...(readRoots ?? []), argv[0]],
      domains: [],
    };
    const env = {
      HOME: dirname(spec),
      ...Object.fromEntries(
        Object.entries(process.env).filter(([key]) => key.startsWith("WHIRLWIND_PROCESS_")),
      ),
      PATH: process.env.PATH || [dirname(process.execPath), "/usr/bin", "/bin"].join(delimiter),
      TMPDIR: process.env.TMPDIR || "/private/tmp",
      WHIRLWIND_NONO_CLI: nono,
    };
    const result = spawnSync(
      process.execPath,
      [
        launcher,
        JSON.stringify(profile),
        "--",
        process.execPath,
        commandRunner,
        JSON.stringify(check.environment ?? {}),
        ...argv,
      ],
      {
        cwd,
        env,
        encoding: "utf8",
        timeout: (check.timeout_seconds || 600) * 1000,
        maxBuffer: 64 * 1024,
      },
    );
    const detail = [result.stdout, result.stderr].filter(Boolean).join("\n").trim();
    if (result.error || (result.status !== 0 && result.status !== 86)) {
      throw new Error(
        check.name +
          ": " +
          (result.error?.message ?? "check runner failed") +
          (detail ? "\n" + detail : ""),
      );
    }
    if (result.status === 0) {
      passed.add(check.name);
    } else {
      output.push(check.name + " failed" + (detail ? ":\n" + detail : "") + "\n");
    }
  }
  return {
    status: passed.size === hook.checks.length ? "passed" : "failed",
    output: output.join("").slice(0, 32 * 1024),
  };
}

let result;
try {
  if (process.env.NONO_HOOK_TYPE !== hook.event)
    throw new Error("Native hook event does not match configuration");
  if (hook.event === "after" && process.env.NONO_EXIT_CODE !== "0")
    throw new Error("Native hook sentinel failed");
  result = runChecks();
} catch (error) {
  result = { status: "error", output: error.message.slice(0, 32 * 1024) };
}
writeFileSync(verdict, JSON.stringify(result), { mode: 0o600, flag: "wx" });
