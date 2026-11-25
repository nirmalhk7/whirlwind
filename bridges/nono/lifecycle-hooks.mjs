import { readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const dispatcher = fileURLToPath(new URL("./run-lifecycle-hook.mjs", import.meta.url));
const shellQuote = (value) => "'" + value.replaceAll("'", "'\\''") + "'";

export async function prepareLifecycleHook(hook, control, profile, nono) {
  if (!hook) return undefined;
  if (
    !profile.read_only ||
    !["before", "after"].includes(hook.event) ||
    !Array.isArray(hook.checks) ||
    hook.checks.length === 0
  ) {
    throw new Error("Invalid read-only lifecycle hook profile");
  }
  const spec = join(control, "hook.json");
  const verdict = join(control, "hook-result.json");
  const script = join(control, "hook.sh");
  await writeFile(
    spec,
    JSON.stringify({ hook, verdict, cwd: profile.cwd, nono, readRoots: profile.read_roots }),
    { mode: 0o600, flag: "wx" },
  );
  await writeFile(
    script,
    "#!/bin/sh\nexec " + [process.execPath, dispatcher, spec].map(shellQuote).join(" ") + "\n",
    { mode: 0o700, flag: "wx" },
  );
  return { verdict, event: hook.event, hooks: { [hook.event]: { script } } };
}

export async function lifecycleHookExit(hook, status) {
  if (!hook || status !== 0) return status;
  const result = JSON.parse(await readFile(hook.verdict, "utf8"));
  if (result.status === "error") throw new Error(result.output);
  if (!["passed", "failed"].includes(result.status))
    throw new Error("Invalid lifecycle hook verdict");
  if (result.output) process.stdout.write(result.output);
  return result.status === "passed" ? 0 : 86;
}
