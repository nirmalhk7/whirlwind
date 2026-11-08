import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { chmod, mkdir, mkdtemp, readFile, realpath, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import test from "node:test";

const exec = promisify(execFile);
const entry = join(dirname(dirname(fileURLToPath(import.meta.url))), "bin/whirlwind");

async function fixture(t, exitCode = 0) {
  const root = await realpath(await mkdtemp(join(tmpdir(), "whirlwind dashboard ")));
  t.after(() => rm(root, { recursive: true, force: true }));
  await mkdir(join(root, "tools"));
  const program = `#!${process.execPath}\nconsole.log(JSON.stringify({args:process.argv.slice(2),cwd:process.cwd(),data:process.env.WHIRLWIND_DATA_DIR}));process.exit(${exitCode});\n`;
  const go = `#!${process.execPath}\nconst fs=require('node:fs');fs.writeFileSync(${JSON.stringify(join(root, "build.json"))},JSON.stringify(process.argv.slice(2)));fs.writeFileSync(process.argv[process.argv.indexOf('-o')+1],${JSON.stringify(program)},{mode:0o755});\n`;
  await writeFile(join(root, "tools/go"), go);
  await chmod(join(root, "tools/go"), 0o755);
  const env = { ...process.env, PATH: `${join(root, "tools")}:${process.env.PATH}`, WHIRLWIND_ROOT: root, WHIRLWIND_DATA_DIR: "saved state", WHIRLWIND_LISTEN: "unused", WHIRLWIND_WEB_PORT: "unused" };
  return { root, env };
}

test("dashboard builds and opens terminal process without frontend or HTTP configuration", async t => {
  const { root, env } = await fixture(t);
  const result = await exec(process.execPath, [entry, "dashboard"], { cwd: tmpdir(), env });
  const opened = JSON.parse(result.stdout.trim());
  assert.equal(opened.cwd, root);
  assert.equal(opened.data, join(root, "saved state"));
  assert.deepEqual(opened.args, []);
  const build = JSON.parse(await readFile(join(root, "build.json"), "utf8"));
  assert.equal(build[0], "build");
  assert.equal(build.at(-1), "./cmd/whirlwind-dashboard");
});

test("dashboard returns terminal application's failure status", async t => {
  const { env } = await fixture(t, 7);
  await assert.rejects(exec(process.execPath, [entry, "dashboard"], { env }), error => error.code === 7);
});

test("Makefile does not expose the retired browser UI target", async () => {
  await assert.rejects(exec("make", ["web"], { cwd: dirname(dirname(fileURLToPath(import.meta.url))) }), error => {
    assert.equal(error.code, 2);
    assert.match(error.stderr, /No rule to make target .*web/);
    return true;
  });
});
