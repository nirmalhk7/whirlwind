import assert from "node:assert/strict";
import { execFile, spawn } from "node:child_process";
import { once } from "node:events";
import { chmod, copyFile, mkdir, mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import http from "node:http";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
import test from "node:test";

const exec = promisify(execFile);
const repo = dirname(dirname(fileURLToPath(import.meta.url)));
const entry = join(repo, "bin/whirlwind");

async function freePort() {
  const server = http.createServer();
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  const port = server.address().port;
  await new Promise((resolve) => server.close(resolve));
  return port;
}

async function fixture(t, extraEnv = {}, timeout = 30000) {
  const root = await mkdtemp(join(tmpdir(), "whirlwind cli test "));
  const data = join(root, "data");
  const backendPort = await freePort();
  const webPort = await freePort();
  await mkdir(join(root, "scripts"));
  await mkdir(join(root, "tools"));
  await mkdir(join(root, "elsewhere"));
  await mkdir(join(root, "web/node_modules/next/dist/bin"), { recursive: true });
  await copyFile(join(repo, "scripts/dev.mjs"), join(root, "scripts/dev.mjs"));
  await writeFile(join(root, "go.mod"), "module whirlwind\n\ngo 1.26.9\n");
  const server = `import http from 'node:http';
import { spawn } from 'node:child_process';
import { writeFileSync } from 'node:fs';
const backend = process.argv.includes('--listen');
if (!backend && process.env.FAIL_FRONTEND) {
  await new Promise((resolve) => setTimeout(resolve, 300));
  process.exit(7);
}
const port = backend ? process.argv[process.argv.indexOf('--listen') + 1].split(':').at(-1) : process.argv[process.argv.indexOf('--port') + 1];
const child = spawn(process.execPath, ['-e', 'setInterval(() => {}, 1000)'], { stdio: 'ignore' });
writeFileSync(process.env.WHIRLWIND_DATA_DIR + '/' + (backend ? 'backend' : 'frontend') + '.json', JSON.stringify({ pid: process.pid, child: child.pid }));
const app = http.createServer((req, res) => {
  if (!backend && process.env.FRONTEND_REDIRECT && req.url === '/') { res.statusCode = 307; res.setHeader('location', '/tasks'); res.end(); return; }
  if (req.url === '/api/v1/health') { res.setHeader('content-type', 'application/json'); res.end(JSON.stringify({ status: 'ok' })); }
  else if (backend) { res.statusCode = 404; res.end(); }
  else { res.end('Whirlwind UI'); }
});
app.listen(Number(port), '127.0.0.1');
`;
  await writeFile(join(root, "web/node_modules/next/dist/bin/next"), server);
  await writeFile(join(root, "tools/go"), `#!${process.execPath}\nconst fs = require('node:fs');\nif (process.argv[2] === 'version') { console.log('go version go1.27.1'); }\nelse { fs.writeFileSync(process.argv[process.argv.indexOf('-o') + 1], ${JSON.stringify(`#!${process.execPath}\n${server}`)}, { mode: 0o755 }); }\n`);
  await chmod(join(root, "tools/go"), 0o755);
  const env = {
    ...process.env,
    PATH: `${join(root, "tools")}:${process.env.PATH}`,
    WHIRLWIND_ROOT: root,
    WHIRLWIND_DATA_DIR: data,
    WHIRLWIND_LISTEN: `127.0.0.1:${backendPort}`,
    WHIRLWIND_WEB_PORT: String(webPort),
    ...extraEnv,
  };
  delete env.WHIRLWIND_BACKEND_URL;
  const run = async (...args) => {
    try {
      const result = await exec(process.execPath, [entry, ...args], { cwd: join(root, "elsewhere"), env, timeout });
      return { code: 0, output: result.stdout + result.stderr };
    } catch (error) {
      return { code: error.code, output: (error.stdout ?? "") + (error.stderr ?? "") };
    }
  };
  t.after(async () => {
    await run("stop");
    await rm(root, { recursive: true, force: true });
  });
  return { root, data, env, run, backendPort, webPort };
}

function alive(pid) {
  try { process.kill(pid, 0); return true; }
  catch (error) { if (error.code === "ESRCH") return false; throw error; }
}

async function waitGone(pid) {
  for (let i = 0; i < 100; i++) {
    if (!alive(pid)) return;
    await new Promise((resolve) => setTimeout(resolve, 30));
  }
  assert.fail(`process ${pid} survived shutdown`);
}

test("help and invalid commands work without starting services", async (t) => {
  const { run } = await fixture(t);
  const help = await run("--help");
  assert.equal(help.code, 0, help.output);
  for (const command of ["start", "status", "stop", "restart", "doctor"]) assert.match(help.output, new RegExp(command));
  assert.equal((await run("wat")).code, 1);
  assert.equal((await run("start", "--typo")).code, 1);
});

test("start, status, restart, stop manage both services from unrelated cwd", async (t) => {
  const { run, data, root } = await fixture(t);
  assert.equal((await run("status")).code, 1);
  let result = await run("start");
  assert.equal(result.code, 0, result.output);
  assert.match(result.output, /http:\/\/127\.0\.0\.1:/);
  const first = JSON.parse(await readFile(join(data, "backend.json"), "utf8"));
  const firstWeb = JSON.parse(await readFile(join(data, "frontend.json"), "utf8"));
  result = await run("status");
  assert.equal(result.code, 0, result.output);
  assert.match(result.output, /backend.*healthy/i);
  assert.match(result.output, /frontend.*healthy/i);
  assert.match(result.output, new RegExp(root));
  result = await run("start");
  assert.equal(result.code, 0, result.output);
  assert.equal(JSON.parse(await readFile(join(data, "backend.json"), "utf8")).pid, first.pid);
  result = await run("restart");
  assert.equal(result.code, 0, result.output);
  assert.notEqual(JSON.parse(await readFile(join(data, "backend.json"), "utf8")).pid, first.pid);
  for (const pid of [first.pid, first.child, firstWeb.pid, firstWeb.child]) await waitGone(pid);
  const second = JSON.parse(await readFile(join(data, "backend.json"), "utf8"));
  const secondWeb = JSON.parse(await readFile(join(data, "frontend.json"), "utf8"));
  assert.equal((await run("stop")).code, 0);
  for (const pid of [second.pid, second.child, secondWeb.pid, secondWeb.child]) await waitGone(pid);
  assert.equal((await run("stop")).code, 0);
  assert.equal((await run("status")).code, 1);
});

test("concurrent starts serialize and preserve one running instance", async (t) => {
  const { run } = await fixture(t);
  const results = await Promise.all([run("start"), run("start")]);
  for (const result of results) assert.equal(result.code, 0, result.output);
  assert.equal((await run("status")).code, 0);
});

test("occupied ports fail without terminating unrelated listener", async (t) => {
  const { run, webPort } = await fixture(t);
  const server = http.createServer((req, res) => res.end("unrelated"));
  server.listen(webPort, "127.0.0.1");
  await once(server, "listening");
  t.after(() => new Promise((resolve) => server.close(resolve)));
  const result = await run("start");
  assert.equal(result.code, 1, result.output);
  assert.match(result.output, /port|in use|occupied/i);
  assert.equal(await (await fetch(`http://127.0.0.1:${webPort}`)).text(), "unrelated");
});

test("failed frontend startup cleans backend and reports log location", async (t) => {
  const { run, data } = await fixture(t, { FAIL_FRONTEND: "1" });
  const result = await run("start");
  assert.equal(result.code, 1, result.output);
  assert.match(result.output, /log/i);
  const backend = JSON.parse(await readFile(join(data, "backend.json"), "utf8"));
  await waitGone(backend.pid);
  await waitGone(backend.child);
  assert.equal((await run("status")).code, 1);
});

test("stale state never signals unrelated PID and can be restarted", async (t) => {
  const { run, data } = await fixture(t);
  const other = spawn(process.execPath, ["-e", "setInterval(() => {}, 1000)"], { stdio: "ignore" });
  t.after(() => other.kill());
  await mkdir(join(data, "launcher"), { recursive: true });
  await writeFile(join(data, "launcher/state.json"), JSON.stringify({ version: 1, token: "a".repeat(32), pid: other.pid }));
  assert.equal((await run("stop")).code, 0);
  assert.equal(alive(other.pid), true);
  const result = await run("start");
  assert.equal(result.code, 0, result.output);
  assert.equal(alive(other.pid), true);
});

test("doctor checks prerequisites and reports missing frontend dependencies", async (t) => {
  const { run, root } = await fixture(t);
  let result = await run("doctor");
  assert.equal(result.code, 0, result.output);
  assert.match(result.output, /go|Go/);
  assert.match(result.output, /Node/);
  await rm(join(root, "web/node_modules"), { recursive: true });
  result = await run("doctor");
  assert.equal(result.code, 1, result.output);
  assert.match(result.output, /npm ci --prefix web/);
});

test("executable symlink resolves checkout from unrelated directory", async (t) => {
  const { root, env } = await fixture(t);
  const link = join(root, "tools/whirlwind");
  await symlink(entry, link);
  delete env.WHIRLWIND_ROOT;
  let result;
  try { await exec(link, ["status"], { cwd: tmpdir(), env }); }
  catch (error) { result = error; }
  assert.equal(result.code, 1);
  assert.ok(result.stdout.includes(`Checkout: ${repo}`), result.stdout);
});


test("stop recovers child groups after supervisor is killed", async (t) => {
  const { run, data } = await fixture(t);
  const result = await run("start");
  assert.equal(result.code, 0, result.output);
  const state = JSON.parse(await readFile(join(data, "launcher/state.json"), "utf8"));
  const backend = JSON.parse(await readFile(join(data, "backend.json"), "utf8"));
  const frontend = JSON.parse(await readFile(join(data, "frontend.json"), "utf8"));
  process.kill(state.pid, "SIGKILL");
  await waitGone(state.pid);
  const status = await run("status");
  assert.equal(status.code, 1);
  assert.match(status.output, /orphaned/);
  assert.equal((await run("stop")).code, 0);
  for (const pid of [backend.pid, backend.child, frontend.pid, frontend.child]) await waitGone(pid);
});

test("invalid network configuration fails before starting processes", async (t) => {
  const { run } = await fixture(t, { WHIRLWIND_LISTEN: "0.0.0.0:9877" });
  const result = await run("start");
  assert.equal(result.code, 1);
  assert.match(result.output, /loopback/);
});


test("frontend page redirects count as a responsive UI", async (t) => {
  const { run } = await fixture(t, { FRONTEND_REDIRECT: "1" }, 5000);
  const result = await run("start");
  assert.equal(result.code, 0, result.output);
  assert.equal((await run("status")).code, 0);
});
