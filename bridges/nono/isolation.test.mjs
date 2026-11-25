import test from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, mkdir, writeFile, readFile, rm, realpath, symlink } from "node:fs/promises";
import { tmpdir, platform, release, arch } from "node:os";
import { join, resolve } from "node:path";
import { spawn, spawnSync } from "node:child_process";
import { createSocket } from "node:dgram";

const launcher = resolve("bridges/nono/launch.mjs");

function run(command, args, cwd) {
  return new Promise((ok, bad) => {
    const child = spawn(command, args, { cwd, stdio: ["ignore", "pipe", "pipe"] });
    let stdout = "",
      stderr = "";
    child.stdout.on("data", (data) => (stdout += data));
    child.stderr.on("data", (data) => (stderr += data));
    const timer = setTimeout(() => child.kill("SIGTERM"), 20000);
    child.once("error", (error) => {
      clearTimeout(timer);
      bad(error);
    });
    child.once("close", (status, signal) => {
      clearTimeout(timer);
      ok({ status, signal, stdout, stderr });
    });
  });
}
function native(profile, probe, args = []) {
  return run(
    process.execPath,
    [launcher, JSON.stringify(profile), "--", process.execPath, "-e", probe, ...args],
    profile.cwd,
  );
}
function git(cwd, ...args) {
  const result = spawnSync("git", ["-C", cwd, ...args], { encoding: "utf8" });
  assert.equal(result.status, 0, result.stderr);
  return result.stdout.trim();
}
function versions(t) {
  const result = spawnSync(process.env.WHIRLWIND_NONO_CLI || "nono", ["--version"], {
    encoding: "utf8",
  });
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout.trim(), "nono 0.81.0");
  t.diagnostic(
    `${platform()} ${release()} ${arch()}; ${result.stdout.trim()}; node ${process.version}`,
  );
}

// Removing shared-metadata isolation or following a scoped symlink with lexical grants breaks this test.
test("native nono protects linked-worktree shared refs hooks config and sibling worktrees", async (t) => {
  versions(t);
  const root = await realpath(await mkdtemp(join(tmpdir(), "ww-linked-native-")));
  try {
    const repo = join(root, "repo"),
      cwd = join(root, "selected"),
      sibling = join(root, "sibling"),
      output = join(root, "output");
    await mkdir(repo);
    await mkdir(output);
    git(repo, "init", "--quiet", "--initial-branch=source");
    await mkdir(join(repo, "src"));
    await writeFile(join(repo, "src", "feature.txt"), "source\n");
    git(repo, "add", ".");
    git(
      repo,
      "-c",
      "user.name=Fixture",
      "-c",
      "user.email=fixture@example.invalid",
      "commit",
      "--quiet",
      "-m",
      "Fixture",
    );
    git(repo, "worktree", "add", "--quiet", "-b", "selected", cwd);
    git(repo, "worktree", "add", "--quiet", "-b", "sibling", sibling);
    git(repo, "config", "extensions.worktreeConfig", "true");
    git(cwd, "config", "--worktree", "fixture.scope", "selected");
    git(sibling, "config", "--worktree", "fixture.scope", "sibling");
    const common = join(repo, ".git"),
      hook = join(common, "hooks", "fixture-hook");
    await writeFile(hook, "fixture hook\n");
    const targets = [
      join(cwd, ".git"),
      join(common, "config"),
      join(common, "HEAD"),
      join(common, "refs", "heads", "source"),
      hook,
      join(common, "worktrees", "selected", "HEAD"),
      join(common, "worktrees", "sibling", "HEAD"),
      join(repo, "src", "feature.txt"),
      join(sibling, "src", "feature.txt"),
      join(common, "refs", "heads", "selected"),
      join(common, "refs", "heads", "sibling"),
      join(common, "worktrees", "selected", "config.worktree"),
      join(common, "worktrees", "sibling", "config.worktree"),
    ];
    const before = await Promise.all(targets.map((path) => readFile(path)));
    // Every protected target really exists and is writable before native policy is applied.
    for (let n = 0; n < targets.length; n++) await writeFile(targets[n], before[n]);
    const links = [join(cwd, "src", "shared-ref"), join(cwd, "src", "sibling-file")];
    await symlink(targets[3], links[0]);
    await symlink(targets[8], links[1]);
    const probe = `const fs=require('fs');const paths=JSON.parse(process.argv[1]);for(const path of paths){try{fs.appendFileSync(path,'unauthorized');throw new Error('write escaped: '+path)}catch(error){if(!['EPERM','EACCES'].includes(error.code))throw error}}fs.writeFileSync('src/accepted.txt','scoped');fs.writeFileSync(process.argv[2],'output');console.log(JSON.stringify({denied:paths.length,scoped:true}));`;
    const profile = {
      cwd,
      read_only: false,
      allowed_paths: ["**"],
      read_roots: [common],
      write_roots: [output],
      domains: [],
      environment: [],
    };
    const result = await native(profile, probe, [
      JSON.stringify([...targets, ...links]),
      join(output, "result"),
    ]);
    assert.equal(result.status, 0, result.stderr || result.stdout);
    assert.deepEqual(JSON.parse(result.stdout), { denied: 15, scoped: true });
    for (let n = 0; n < targets.length; n++)
      assert.deepEqual(await readFile(targets[n]), before[n], targets[n]);
    assert.equal(await readFile(join(cwd, "src", "accepted.txt"), "utf8"), "scoped");
    assert.equal(await readFile(join(output, "result"), "utf8"), "output");
    assert.equal(git(cwd, "rev-parse", "--abbrev-ref", "HEAD"), "selected");
    assert.equal(git(sibling, "rev-parse", "--abbrev-ref", "HEAD"), "sibling");
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

const udpProbe = `const socket=require('dgram').createSocket('udp4');socket.on('error',error=>{if(!['EPERM','EACCES'].includes(error.code))throw error;console.log('denied');socket.close()});socket.send(Buffer.from('native-udp-control'),Number(process.argv[1]),'127.0.0.1',error=>{if(error){if(!['EPERM','EACCES'].includes(error.code))throw error;console.log('denied')}else console.log('sent');socket.close()});`;
const dnsProbe = `const resolver=new (require('dns').Resolver)({timeout:300,tries:1});resolver.setServers(['127.0.0.1:'+process.argv[1]]);resolver.resolve4('native-probe.invalid',(error,addresses)=>{if(error){if(!['ECONNREFUSED','ETIMEOUT','EACCES','EPERM'].includes(error.code))throw error;console.log('denied')}else console.log(addresses.join(','))});`;
const sshProbe = `const socket=require('net').connect({host:process.argv[1],port:22});socket.on('connect',()=>{throw new Error('direct SSH/22 escaped')});socket.on('error',error=>{if(!['EPERM','EACCES'].includes(error.code))throw error;console.log('denied')});`;

// Direct sockets must remain blocked even when HTTP egress is enabled, not merely in deny-all mode.
test("native nono blocks Coding UDP DNS and outbound SSH/22 bypasses with egress enabled", async (t) => {
  versions(t);
  const cwd = await realpath(await mkdtemp(join(tmpdir(), "ww-network-native-"))),
    socket = createSocket("udp4");
  let packets = 0;
  socket.on("message", (message, remote) => {
    packets++;
    if (message.toString() === "native-udp-control") return;
    // Minimal positive DNS A response for the controlled query, no upstream resolver.
    const response = Buffer.concat([
      message,
      Buffer.from([0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 1, 0, 4, 192, 0, 2, 7]),
    ]);
    response.writeUInt16BE(0x8180, 2);
    response.writeUInt16BE(1, 6);
    socket.send(response, remote.port, remote.address);
  });
  await new Promise((ok, bad) => {
    socket.once("error", bad);
    socket.bind(0, "127.0.0.1", ok);
  });
  try {
    const port = String(socket.address().port);
    const udpControl = await run(process.execPath, ["-e", udpProbe, port], cwd);
    assert.equal(udpControl.status, 0, udpControl.stderr);
    assert.equal(udpControl.stdout.trim(), "sent");
    const dnsControl = await run(process.execPath, ["-e", dnsProbe, port], cwd);
    assert.equal(dnsControl.status, 0, dnsControl.stderr);
    assert.equal(dnsControl.stdout.trim(), "192.0.2.7");
    assert.equal(packets, 2, "unconfined UDP and DNS controls must reach fixture");
    const profile = {
      cwd,
      read_only: false,
      allowed_paths: ["fixture-output.txt"],
      read_roots: [],
      domains: ["allowed.whirlwind.invalid"],
      environment: [],
    };
    for (const [name, probe, args] of [
      ["UDP", udpProbe, [port]],
      ["DNS", dnsProbe, [port]],
      ["DNS/53", udpProbe, ["53"]],
      ["loopback SSH/22", sshProbe, ["127.0.0.1"]],
      ["outbound SSH/22", sshProbe, ["192.0.2.7"]],
    ]) {
      const result = await native(profile, probe, args);
      assert.equal(result.status, 0, `${name}: ${result.stderr || result.stdout}`);
      assert.equal(result.stdout.trim(), "denied", name);
    }
    assert.equal(packets, 2, "sandboxed UDP and DNS must produce no network effect");
  } finally {
    socket.close();
    await rm(cwd, { recursive: true, force: true });
  }
});
