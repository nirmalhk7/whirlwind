import assert from "node:assert/strict";
import { mkdtemp, realpath, rm, symlink } from "node:fs/promises";
import { homedir, tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { launcherPaths } from "./launcher-paths.mjs";
import { configuration } from "./launcher-config.mjs";
import { localConfiguration } from "./local-supervisor.mjs";

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

test("legacy ownership and supervisor configuration agree on explicit paths", async (t) => {
  const root = await realpath(await mkdtemp(join(tmpdir(), "whirlwind config ")));
  t.after(() => rm(root, { recursive: true, force: true }));
  const overrides = {
    WHIRLWIND_ROOT: root,
    WHIRLWIND_DATA_DIR: "state",
    WHIRLWIND_SETTINGS_FILE: "custom.yaml",
    WHIRLWIND_LISTEN: "127.0.0.1:9877",
    WHIRLWIND_WEB_PORT: "9876",
  };
  const saved = Object.fromEntries(Object.keys(overrides).map((key) => [key, process.env[key]]));
  try {
    Object.assign(process.env, overrides);
    const legacy = await configuration(),
      supervisor = await localConfiguration();
    assert.equal(legacy.root, supervisor.root);
    assert.equal(legacy.data, supervisor.data);
    assert.equal(legacy.env.WHIRLWIND_SETTINGS_FILE, supervisor.settings);
    assert.equal(supervisor.data, join(root, "state"));
  } finally {
    for (const [key, value] of Object.entries(saved)) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
  }
});
