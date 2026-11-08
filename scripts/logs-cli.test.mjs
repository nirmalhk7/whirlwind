import assert from "node:assert/strict";
import { execFile, spawn } from "node:child_process";
import { appendFile, mkdir, mkdtemp, rename, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import test from "node:test";

const exec = promisify(execFile);
const root = dirname(dirname(fileURLToPath(import.meta.url)));
const entry = join(root, "bin/whirlwind");

async function fixture(t) {
  const data = await mkdtemp(join(tmpdir(), "whirlwind-logs-"));
  t.after(() => rm(data, { recursive: true, force: true }));
  const env = { ...process.env, WHIRLWIND_ROOT: root, WHIRLWIND_DATA_DIR: data };
  const run = (...args) => exec(process.execPath, [entry, ...args], { env, cwd: tmpdir(), timeout: 5000 });
  return { data, env, run };
}

test("logs prints full supervisor and launcher history without a running supervisor", async t => {
  const { data, run } = await fixture(t);
  await mkdir(join(data, "launcher"));
  const history = Array.from({ length: 250 }, (_, i) => `supervisor entry ${i}\n`).join("");
  await writeFile(join(data, "whirlwind-supervisor.log"), history);
  await writeFile(join(data, "launcher/whirlwind.log"), "legacy launcher output\n");
  const { stdout } = await run("logs");
  assert.ok(stdout.includes(history), "complete history was truncated or changed");
  assert.match(stdout, /legacy launcher output/);
  assert.match(stdout, /whirlwind-supervisor\.log/);
  assert.match(stdout, /launcher\/whirlwind\.log/);
});

test("logs reports an empty log history and rejects unknown options", async t => {
  const { run } = await fixture(t);
  const result = await run("logs");
  assert.equal(result.stdout, "");
  assert.match(result.stderr, /no .*logs/i);
  await assert.rejects(run("logs", "--typo"), error => error.code === 1);
});

function follow(t, env) {
  const child = spawn(process.execPath, [entry, "logs", "--follow"], { env, cwd: tmpdir(), stdio: ["ignore", "pipe", "pipe"] });
  let output = "", errors = "";
  child.stdout.setEncoding("utf8").on("data", chunk => { output += chunk; });
  child.stderr.setEncoding("utf8").on("data", chunk => { errors += chunk; });
  const done = new Promise(resolve => child.once("close", (code, signal) => resolve({ code, signal })));
  t.after(async () => { if (child.exitCode === null && child.signalCode === null) child.kill("SIGTERM"); await done; });
  const waitFor = async text => {
    const deadline = Date.now() + 5000;
    while (!output.includes(text) && Date.now() < deadline && child.exitCode === null) {
      await new Promise(resolve => setTimeout(resolve, 20));
    }
    assert.ok(output.includes(text), `missing ${text}; stdout=${output}; stderr=${errors}`);
  };
  return { child, done, waitFor, output: () => output };
}

test("logs --follow prints history and follows every log without duplicating entries", async t => {
  const { data, env } = await fixture(t);
  await mkdir(join(data, "launcher"));
  const supervisor = join(data, "whirlwind-supervisor.log");
  const launcher = join(data, "launcher/whirlwind.log");
  await writeFile(supervisor, "historical entry\n");
  const stream = follow(t, env);
  await stream.waitFor("historical entry");
  await appendFile(supervisor, "new supervisor entry\n");
  await stream.waitFor("new supervisor entry");
  await writeFile(launcher, "new launcher entry\n");
  await stream.waitFor("new launcher entry");
  await rename(supervisor, supervisor + ".1");
  await writeFile(supervisor, "after rotation\n");
  await stream.waitFor("after rotation");
  await writeFile(supervisor, "short\n");
  await stream.waitFor("short");
  stream.child.kill("SIGINT");
  assert.deepEqual(await stream.done, { code: 0, signal: null });
  for (const entry of ["historical entry", "new supervisor entry", "new launcher entry", "after rotation"]) {
    assert.equal(stream.output().split(entry).length - 1, 1, `${entry} was duplicated`);
  }
});

test("logs --follow waits for the first log file to be created", async t => {
  const { data, env } = await fixture(t);
  const stream = follow(t, env);
  await new Promise(resolve => setTimeout(resolve, 100));
  await writeFile(join(data, "whirlwind-supervisor.log"), "first available log\n");
  await stream.waitFor("first available log");
});
