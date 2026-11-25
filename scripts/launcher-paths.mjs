import { realpath } from "node:fs/promises";
import { homedir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

// Shared by the supervisor and dashboard.
// Relative overrides resolve against the real checkout, never the caller's cwd.
export async function launcherPaths(env = process.env) {
  const root = await realpath(
    env.WHIRLWIND_ROOT || dirname(dirname(fileURLToPath(import.meta.url))),
  );
  return {
    root,
    data: resolve(
      root,
      env.WHIRLWIND_DATA_DIR || join(homedir(), ".local/share/whirlwind/parity-v1"),
    ),
    settings: resolve(root, env.WHIRLWIND_SETTINGS_FILE || "settings.yaml"),
  };
}
