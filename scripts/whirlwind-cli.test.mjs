import assert from "node:assert/strict";
import { execFile, spawn, spawnSync } from "node:child_process";
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
    // A container's PID 1 can leave exited orphans in the process table.
    if (process.platform === "linux") {
      try {
        const [state] = await linuxProcessStat(pid);
        if (state === "Z" || state === "X") return;
      } catch (error) { if (error.code === "ENOENT") return; throw error; }
    }
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  assert.fail(`process ${pid} survived shutdown`);
}

async function linuxProcessStat(pid) {
  const stat = await readFile(`/proc/${pid}/stat`, "utf8");
  return stat.slice(stat.lastIndexOf(")") + 1).trim().split(/\s+/);
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
  const verifier=join(data,"runtime","whirlwind-verify");
  await access(verifier);
  const rejected=spawnSync(verifier,[],{input:'{}',encoding:'utf8',timeout:10000});
  assert.equal(rejected.status,1,rejected.stderr);
  assert.match(rejected.stderr,/verification/i);
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

test("Linux launcher restart reaps a registered worker and preserves unrelated processes", { skip: process.platform !== "linux" }, async t => {
  const { run, data } = await fixture(t);
  const start = await run("start");
  assert.equal(start.code, 0, start.output);

  const worker = spawn(process.execPath, ["-e", "setInterval(() => {}, 1000)"], { detached: true, stdio: "ignore" });
  const unrelated = spawn(process.execPath, ["-e", "setInterval(() => {}, 1000)"], { detached: true, stdio: "ignore" });
  t.after(() => { worker.kill(); unrelated.kill(); });
  const started = (await linuxProcessStat(worker.pid))[19];
  assert.match(started, /^\d+$/);

  const directory = join(data, "processes");
  await mkdir(directory, { mode: 0o700, recursive: true });
  const token = "e".repeat(32);
  const registration = join(directory, `${token}.json`);
  await writeFile(registration, JSON.stringify({ pid: worker.pid, token, started }), { mode: 0o600 });

  const restarted = await run("restart");
  assert.equal(restarted.code, 0, restarted.output);
  await waitGone(worker.pid);
  await assert.rejects(access(registration), { code: "ENOENT" });
  assert.equal(processAlive(unrelated.pid), true);
  assert.equal((await run("status")).code, 0);
});

test("managed policy approval binds the file and project without touching either checkout", async t => {
  const { run, data, env } = await fixture(t);
  const first = join(data, "first-checkout");
  const second = join(data, "second-checkout");
  const createCheckout = async directory => {
    await mkdir(directory);
    await exec("git", ["-C", directory, "init", "--quiet"]);
    await writeFile(join(directory, "result.txt"), "fixture\n");
    await exec("git", ["-C", directory, "add", "result.txt"]);
    await exec("git", ["-C", directory, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "core.hooksPath=/dev/null", "commit", "--no-gpg-sign", "-qm", "Fixture"]);
  };
  const gitState = async directory => ({
    head: (await exec("git", ["-C", directory, "rev-parse", "HEAD"])).stdout,
    status: (await exec("git", ["-C", directory, "status", "--porcelain", "--untracked-files=all"])).stdout,
    index: await readFile(join(directory, ".git", "index")),
    config: await readFile(join(directory, ".git", "config")),
  });
  await createCheckout(first);
  await createCheckout(second);
  const before = await Promise.all([gitState(first), gitState(second)]);
  await writeFile(env.WHIRLWIND_SETTINGS_FILE, JSON.stringify({ version: 1, workers: { stopped: true }, providers: [], agents: {}, projects: [first, second] }));
  const file = join(data, "reviewed.yaml");
  const policy = "allowed_paths: [result.txt]\nrisk_rules: [{level: low, paths: [result.txt]}]\nchecks: [{name: result, command: [test, -f, result.txt]}]\n";
  await writeFile(file, policy);
  const start = await run("start");
  assert.equal(start.code, 0, start.output);

  const preview = await run("policy", "preview", first, file);
  assert.equal(preview.code, 0, preview.output);
  assert.ok(preview.output.includes(policy));
  const [, version, hash] = /Version (\d+)\nSHA256 ([0-9a-f]{64})/.exec(preview.output);
  const approve = project => run("policy", "approve", project, file, version, hash);
  assert.equal((await approve(second)).code, 1);
  await writeFile(file, policy + "# Changed after preview\n");
  assert.equal((await approve(first)).code, 1);
  await writeFile(file, policy);
  const approved = await approve(first);
  assert.equal(approved.code, 0, approved.output);
  assert.match(approved.output, new RegExp(hash));
  assert.equal((await approve(first)).code, 1);

  const restart = await run("restart");
  assert.equal(restart.code, 0, restart.output);
  const reopened = await run("policy", "preview", first, file);
  assert.equal(reopened.code, 0, reopened.output);
  assert.ok(reopened.output.includes(`Version ${Number(version) + 1}\nSHA256 ${hash}`));
  assert.deepEqual(await Promise.all([gitState(first), gitState(second)]), before);
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

test("stop rejects a malformed legacy process registration", async t => {
  const { data, run } = await fixture(t);
  const statePath = join(data, "launcher/state.json");
  await mkdir(dirname(statePath), { recursive: true });
  const registration = JSON.stringify({ version: 1, token: "e".repeat(32), pid: -1 });
  await writeFile(statePath, registration);
  const stopped = await run("stop");
  assert.equal(stopped.code, 1, stopped.output);
  assert.match(stopped.output, /invalid process registration/);
  assert.equal(await readFile(statePath, "utf8"), registration);
});

test("stop waits for the owned process to finish after its control socket closes", async t => {
  const {run,data,env}=await fixture(t);
  const token="d".repeat(32);
  const key=createHash("sha256").update(data).digest("hex").slice(0,16);
  const socketPath=join("/tmp",`whirlwind-${process.getuid()}`,`${key}.sock`);
  const marker=join(data,"cleanup-finished");
  await mkdir(dirname(socketPath),{recursive:true,mode:0o700});
  const source=`
    const net=require('node:net'),fs=require('node:fs');
    const server=net.createServer(socket=>socket.on('data',data=>{
      const request=JSON.parse(data.toString());
      socket.end(JSON.stringify({request_id:request.request_id,result:request.operation==='supervisor.stop'?{status:'stopping'}:[]})+'\\n');
      if(request.operation==='supervisor.stop') server.close(()=>setTimeout(()=>{fs.writeFileSync(process.argv[2],'complete');process.exit(0)},1200));
    }));
    server.listen(process.argv[1],()=>process.stdout.write('ready\\n'));
  `;
  const child=spawn(process.execPath,["-e",source,socketPath,marker],{env:{...env,WHIRLWIND_SUPERVISOR_TOKEN:token},stdio:["ignore","pipe","pipe"]});
  t.after(()=>{if(processAlive(child.pid))child.kill();});
  await new Promise((resolve,reject)=>{child.stdout.once("data",resolve);child.once("error",reject);child.once("exit",code=>reject(new Error(`control peer exited ${code}`)));});
  const statePath=join(data,"control/supervisor.json");
  await mkdir(dirname(statePath),{recursive:true});
  await writeFile(statePath,JSON.stringify({version:1,token,pid:child.pid,root:repo,startedAt:new Date().toISOString()}));
  const stopped=await run("stop");
  assert.equal(stopped.code,0,stopped.output);
  assert.equal(processAlive(child.pid),false,"stop returned while owned process still cleaning up");
  assert.equal(await readFile(marker,"utf8"),"complete");
  await assert.rejects(access(statePath),{code:"ENOENT"});
});
