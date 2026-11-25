import assert from "node:assert/strict";
import { mkdtemp, realpath, rm, symlink } from "node:fs/promises";
import { homedir, tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { launcherPaths } from "./launcher-paths.mjs";

test("launcher defaults retain parity state and checkout settings", async () => {
  const root = await realpath(dirname(dirname(fileURLToPath(import.meta.url))));
  assert.deepEqual(await launcherPaths({}), {
    root,
    data: join(homedir(), ".local/share/whirlwind/parity-v1"),
    settings: join(root, "settings.yaml"),
  });
  assert.deepEqual(
    await launcherPaths({
      WHIRLWIND_ROOT: "",
      WHIRLWIND_DATA_DIR: "",
      WHIRLWIND_SETTINGS_FILE: "",
    }),
    await launcherPaths({}),
  );
});

test("relative overrides follow the real checkout, including symlink roots and spaces", async (t) => {
  const root = await realpath(await mkdtemp(join(tmpdir(), "whirlwind paths ")));
  t.after(() => rm(root, { recursive: true, force: true }));
  const alias = join(root, "alias");
  await symlink(root, alias);
  const env = {
    WHIRLWIND_ROOT: alias,
    WHIRLWIND_DATA_DIR: "saved state",
    WHIRLWIND_SETTINGS_FILE: "config/custom.yaml",
  };
  assert.deepEqual(await launcherPaths(env), {
    root,
    data: join(root, "saved state"),
    settings: join(root, "config/custom.yaml"),
  });
  const data = join(root, "absolute data"),
    settings = join(root, "absolute.yaml");
  assert.deepEqual(
    await launcherPaths({ ...env, WHIRLWIND_DATA_DIR: data, WHIRLWIND_SETTINGS_FILE: settings }),
    { root, data, settings },
  );
  await assert.rejects(launcherPaths({ WHIRLWIND_ROOT: join(root, "missing") }), {
    code: "ENOENT",
  });
});
