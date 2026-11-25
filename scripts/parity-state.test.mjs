import assert from "node:assert/strict";
import test from "node:test";
import { homedir } from "node:os";
import { join } from "node:path";
import { localConfiguration } from "./local-supervisor.mjs";

test("parity launcher isolates new supervisor state without changing explicit overrides", async () => {
  const previous = process.env.WHIRLWIND_DATA_DIR;
  try {
    delete process.env.WHIRLWIND_DATA_DIR;
    assert.equal(
      (await localConfiguration()).data,
      join(homedir(), ".local/share/whirlwind/parity-v1"),
    );
    process.env.WHIRLWIND_DATA_DIR = "/tmp/whirlwind-explicit-state";
    assert.equal((await localConfiguration()).data, "/tmp/whirlwind-explicit-state");
  } finally {
    if (previous === undefined) delete process.env.WHIRLWIND_DATA_DIR;
    else process.env.WHIRLWIND_DATA_DIR = previous;
  }
});
