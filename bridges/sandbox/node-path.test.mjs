import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp, mkdir, writeFile, symlink, rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join, dirname} from 'node:path';
import {spawnSync} from 'node:child_process';

// Removing interpreter PATH resolution must break actual env/shebang execution.
test('sandbox resolves Node behind an unreadable Homebrew-style bin alias', async () => {
 const root=await mkdtemp(join(tmpdir(),'whirlwind-node-path-'));
 try {
  const cwd=join(root,'source'),bin=join(root,'bin');
  await mkdir(cwd);await mkdir(bin);
  await symlink(process.execPath,join(bin,'node'));
  const script=join(cwd,'peer.mjs');
  await writeFile(script,'#!/usr/bin/env node\nconsole.log("interpreter-ready");\n',{mode:0o700});
  const profile={cwd,read_only:true,read_roots:[process.execPath],domains:[]};
  const env={...process.env,PATH:bin+':/usr/bin:/bin'};
  for(const command of [['/usr/bin/env','node','--version'],[script],[process.execPath,'--version'],['/bin/sh','-c','node --version']]) {
   const run=spawnSync(process.execPath,[new URL('./launch.mjs',import.meta.url).pathname,JSON.stringify(profile),'--',...command],{env,encoding:'utf8',timeout:20000});
   assert.equal(run.status,0,`${command.join(' ')}: ${run.stderr}`);
   assert.match(run.stdout,command[0]===script?/interpreter-ready/:/^v\d/);
  }
  assert.notEqual(dirname(process.execPath),bin);
 } finally {await rm(root,{recursive:true,force:true});}
});
