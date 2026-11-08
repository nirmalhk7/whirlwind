import assert from "node:assert/strict";
import { execFile, spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { access, chmod, cp, mkdir, mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import net from "node:net";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
import test from "node:test";

const exec = promisify(execFile);
const repo = dirname(dirname(fileURLToPath(import.meta.url)));
const entry = join(repo, "bin/whirlwind");

async function fixture(t, timeout = 90000) {
  const data = await mkdtemp(join("/tmp", "ww-data-"));
  const env = { ...process.env, WHIRLWIND_ROOT: repo, WHIRLWIND_DATA_DIR: data, WHIRLWIND_SETTINGS_FILE: join(data, "settings.yaml") };
  const runAt = async (executable, commandEnv, ...args) => {
    try {
      const result = await exec(process.execPath, [executable, ...args], { cwd: tmpdir(), env: commandEnv, timeout, maxBuffer: 4 * 1024 * 1024 });
      return { code: 0, output: result.stdout + result.stderr };
    } catch (error) {
      return { code: error.code, output: (error.stdout ?? "") + (error.stderr ?? "") };
    }
  };
  const run = (...args) => runAt(entry, env, ...args);
  t.after(async () => { await run("stop"); await rm(data, { recursive: true, force: true }); });
  return { data, env, run, runAt };
}

async function waitGone(pid) {
  for (let i = 0; i < 100; i++) {
    try { process.kill(pid, 0); } catch (error) { if (error.code === "ESRCH") return; throw error; }
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  assert.fail(`process ${pid} survived shutdown`);
}

function processAlive(pid) {
  try { process.kill(pid, 0); return true; }
  catch (error) { if (error.code === "ESRCH") return false; throw error; }
}

test("help and invalid commands describe the local terminal supervisor", async t => {
  const { run } = await fixture(t);
  const help = await run("--help");
  assert.equal(help.code, 0, help.output);
  for (const command of ["dashboard", "start", "status", "stop", "restart", "doctor"]) assert.match(help.output, new RegExp(command));
  assert.match(help.output, /private local socket/i);
  assert.equal((await run("wat")).code, 1);
  assert.equal((await run("start", "--typo")).code, 1);
});

test("start, status, restart and stop manage the durable local supervisor", async t => {
  const { run, data } = await fixture(t);
  assert.equal((await run("status")).code, 1);
  let result = await run("start");
  assert.equal(result.code, 0, result.output);
  const first = JSON.parse(await readFile(join(data, "control/supervisor.json"), "utf8"));
  assert.equal((await run("status")).code, 0);
  const concurrent = await Promise.all([run("start"), run("start")]);
  for (const item of concurrent) assert.equal(item.code, 0, item.output);
  result = await run("restart");
  assert.equal(result.code, 0, result.output);
  const second = JSON.parse(await readFile(join(data, "control/supervisor.json"), "utf8"));
  assert.notEqual(second.pid, first.pid);
  await waitGone(first.pid);
  assert.equal((await run("stop")).code, 0);
  await waitGone(second.pid);
  assert.equal((await run("status")).code, 1);
  await assert.rejects(access(join(data, "control/supervisor.json")), { code: "ENOENT" });
});

test("supervisor starts and stops from a checkout without web or npm files", async t => {
  const checkout = await mkdtemp(join("/tmp", "ww-no-web-checkout-"));
  const data = await mkdtemp(join("/tmp", "ww-no-web-data-"));
  t.after(async () => { await rm(checkout, { recursive: true, force: true }); await rm(data, { recursive: true, force: true }); });
  for (const name of ["bin", "scripts", "cmd", "internal", "prompts", "bridges"]) {
    await cp(join(repo, name), join(checkout, name), { recursive: true });
  }
  for (const name of ["go.mod", "go.sum"]) await cp(join(repo, name), join(checkout, name));
  await assert.rejects(access(join(checkout, "web")), { code: "ENOENT" });
  await assert.rejects(access(join(checkout, "package.json")), { code: "ENOENT" });
  const go = (await exec("which", ["go"])).stdout.trim();
  const tools = join(checkout, "test-tools");
  await mkdir(tools);
  await symlink(go, join(tools, "go"));
  await symlink("/bin/ps", join(tools, "ps"));
  const env = {
    ...process.env,
    PATH: tools,
    CGO_ENABLED: "0",
    WHIRLWIND_ROOT: checkout,
    WHIRLWIND_DATA_DIR: data,
    WHIRLWIND_SETTINGS_FILE: join(data, "settings.yaml"),
  };
  const run = async (...args) => {
    try {
      const result = await exec(process.execPath, [join(checkout, "bin/whirlwind"), ...args], { cwd: tmpdir(), env, timeout: 90000, maxBuffer: 4 * 1024 * 1024 });
      return { code: 0, output: result.stdout + result.stderr };
    } catch (error) { return { code: error.code, output: (error.stdout ?? "") + (error.stderr ?? "") }; }
  };
  const started = await run("start");
  assert.equal(started.code, 0, started.output);
  assert.equal((await run("status")).code, 0);
  const state = JSON.parse(await readFile(join(data, "control/supervisor.json"), "utf8"));
  const stopped = await run("stop");
  assert.equal(stopped.code, 0, stopped.output);
  await waitGone(state.pid);
  assert.equal((await run("status")).code, 1);
});

test("cold concurrent starts create one supervisor", async t => {
  const { run, data } = await fixture(t);
  const starts = await Promise.all([run("start"), run("start"), run("start")]);
  for (const result of starts) assert.equal(result.code, 0, result.output);
  assert.equal((await run("status")).code, 0);
});

test("failed supervisor build leaves no state or temporary binary and preserves build diagnostics", async t => {
  const { env, data, runAt } = await fixture(t);
  const toolchain = join(data, "test-bin");
  const fakeGo = join(toolchain, "go");
  await mkdir(toolchain, { recursive: true });
  await writeFile(fakeGo, "#!/bin/sh\nprintf 'synthetic build failure\\n' >&2\nexit 17\n", { mode: 0o700 });
  await chmod(fakeGo, 0o700);
  const updated = { ...env, PATH: `${toolchain}:${env.PATH}` };
  const result = await runAt(entry, updated, "start");
  assert.equal(result.code, 1);
  assert.match(result.output, /Logs: .*whirlwind-supervisor\.log/);
  await assert.rejects(access(join(data, "control/supervisor.json")), { code: "ENOENT" });
  const log = await readFile(join(data, "whirlwind-supervisor.log"), "utf8");
  assert.match(log, /synthetic build failure/);
  await assert.rejects(access(join(data, "runtime/whirlwind-supervisor")), { code: "ENOENT" });
  const runtimeEntries = await import("node:fs/promises").then(fs => fs.readdir(join(data, "runtime")));
  assert.deepEqual(runtimeEntries, []);
});


test("a stale PID cannot signal an unrelated process", async t => {
  const { run, data } = await fixture(t);
  const unrelated = spawn(process.execPath, ["-e", "setInterval(() => {}, 1000)"], { stdio: "ignore" });
  t.after(() => unrelated.kill());
  await mkdir(join(data, "launcher"), { recursive: true });
  await writeFile(join(data, "launcher/state.json"), JSON.stringify({ version: 1, token: "a".repeat(32), pid: unrelated.pid }));
  assert.equal((await run("stop")).code, 0);
  assert.equal(processAlive(unrelated.pid), true);
  const result = await run("start");
  assert.equal(result.code, 0, result.output);
  assert.equal(processAlive(unrelated.pid), true);
});

test("a stale supervisor PID cannot signal an unrelated process", async t => {
  const { run, data } = await fixture(t);
  const unrelated = spawn(process.execPath, ["-e", "setInterval(() => {}, 1000)"], { stdio: "ignore" });
  t.after(() => unrelated.kill());
  const statePath = join(data, "control/supervisor.json");
  await mkdir(dirname(statePath), { recursive: true });
  await writeFile(statePath, JSON.stringify({ version: 1, token: "c".repeat(32), pid: unrelated.pid, root: repo, startedAt: new Date().toISOString() }));
  assert.equal((await run("stop")).code, 0);
  assert.equal(processAlive(unrelated.pid), true);
  await assert.rejects(access(statePath), { code: "ENOENT" });
});

test("stop recovers an owned supervisor process after its control socket owner is killed", async t => {
  const { run, data } = await fixture(t);
  const start = await run("start");
  assert.equal(start.code, 0, start.output);
  const statePath = join(data, "control/supervisor.json");
  const state = JSON.parse(await readFile(statePath, "utf8"));
  const orphan = spawn(process.execPath, ["-e", "setInterval(() => {}, 1000)"], {
    env: { ...process.env, WHIRLWIND_SUPERVISOR_TOKEN: state.token },
    detached: true,
    stdio: "ignore",
  });
  t.after(() => { if (processAlive(orphan.pid)) orphan.kill(); });
  process.kill(state.pid, "SIGKILL");
  await waitGone(state.pid);
  const stopped = await run("stop");
  assert.equal(stopped.code, 0, stopped.output);
  await waitGone(orphan.pid);
  await assert.rejects(access(statePath), { code: "ENOENT" });
});

test("executable symlink finds checkout from an unrelated working directory", async t => {
  const { data, env, runAt } = await fixture(t);
  const link = join(data, "whirlwind-link");
  await symlink(entry, link);
  const symlinkEnv = { ...env };
  delete symlinkEnv.WHIRLWIND_ROOT;
  const started = await runAt(link, symlinkEnv, "start");
  assert.equal(started.code, 0, started.output);
  assert.equal((await runAt(link, symlinkEnv, "status")).code, 0);
});

test("an unrelated active Unix socket cannot impersonate the supervisor", async t => {
  const { data, run } = await fixture(t);
  const owner = process.getuid();
  const key = createHash("sha256").update(data).digest("hex").slice(0, 16);
  const socketPath = join("/tmp", `whirlwind-${owner}`, `${key}.sock`);
  await mkdir(dirname(socketPath), { recursive: true, mode: 0o700 });
  const server = net.createServer(socket => socket.on("data", () => socket.end(JSON.stringify({ request_id: "foreign-response", result: [] }) + "\n")));
  await new Promise((resolve, reject) => { server.once("error", reject); server.listen(socketPath, resolve); });
  t.after(() => new Promise(resolve => server.close(resolve)));
  const result = await run("start");
  assert.equal(result.code, 1, result.output);
  assert.match(result.output, /active.*not.*Whirlwind supervisor/i);
  await assert.rejects(access(join(data, "control/supervisor.json")), { code: "ENOENT" });
});

test("legacy browser ownership requires explicit stop before supervisor startup", async t => {
  const { run, data, env } = await fixture(t);
  const token = "b".repeat(32);
  const legacy = spawn(process.execPath, ["-e", "setInterval(() => {}, 1000)"], { stdio: "ignore", env: { ...env, WHIRLWIND_LAUNCHER_TOKEN: token } });
  t.after(() => { if (processAlive(legacy.pid)) legacy.kill(); });
  await mkdir(join(data, "launcher"), { recursive: true });
  await writeFile(join(data, "launcher/state.json"), JSON.stringify({ version: 1, token, pid: legacy.pid, root: repo, backendURL: "http://127.0.0.1:9877", webURL: "http://127.0.0.1:9876", startedAt: new Date().toISOString() }));
  let result = await run("start");
  assert.equal(result.code, 1, result.output);
  assert.match(result.output, /legacy HTTP\/browser instance.*run.*stop/i);
  assert.equal(processAlive(legacy.pid), true);
  result = await run("stop");
  assert.equal(result.code, 0, result.output);
  await waitGone(legacy.pid);
  result = await run("start");
  assert.equal(result.code, 0, result.output);
});
