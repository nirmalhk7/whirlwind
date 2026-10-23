import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, writeFile, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';

// These exercise the real OS boundary: removing the sandbox or widening write/read roots breaks them.
test('real sandbox permits coding writes but denies sibling secrets, outside writes, and raw network', async () => {
 const root=await mkdtemp(join(tmpdir(),'whirlwind-boundary-'));
 try {
  const cwd=join(root,'source'); await mkdir(cwd);
  const secret=join(root,'secret'); await writeFile(secret,'outside-secret');
  const outside=join(root,'escaped');
  const profile={cwd,read_only:false,allowed_paths:['**'],policy_hash:'fixture',read_roots:[],domains:[],environment:[]};
  const code=`const fs=require('fs'); fs.writeFileSync('allowed','yes'); let denied=0; try{fs.readFileSync(${JSON.stringify(secret)})}catch{denied++} try{fs.writeFileSync(${JSON.stringify(outside)},'bad')}catch{denied++} if(denied!==2)process.exit(9); const net=require('net'); const s=net.connect({host:'1.1.1.1',port:443}); s.on('connect',()=>process.exit(10)); s.on('error',()=>process.exit(0)); setTimeout(()=>process.exit(11),2000);`;
  const run=spawnSync(process.execPath,[new URL('./launch.mjs',import.meta.url).pathname,JSON.stringify(profile),'--',process.execPath,'-e',code],{cwd,encoding:'utf8',timeout:20000});
  assert.equal(run.status,0,run.stderr||String(run.error));
  assert.equal(await readFile(join(cwd,'allowed'),'utf8'),'yes');
  await assert.rejects(readFile(outside));
 } finally {await rm(root,{recursive:true,force:true});}
});

test('coding allowlist denies other source paths and symlink escapes',async()=>{
 const root=await mkdtemp(join(tmpdir(),'whirlwind-scope-'));
 try{
  const cwd=join(root,'source');await mkdir(join(cwd,'allowed'),{recursive:true});await mkdir(join(cwd,'blocked'));
  const outside=join(root,'outside');await mkdir(outside);
  const {symlink}=await import('node:fs/promises');await symlink(outside,join(cwd,'allowed','escape'));
  const profile={cwd,read_only:false,allowed_paths:['allowed/**'],read_roots:[],domains:[]};
  const code=`const fs=require('fs');fs.writeFileSync('allowed/good','yes');let denied=0;for(const path of ['blocked/bad','allowed/escape/bad']){try{fs.writeFileSync(path,'bad')}catch{denied++}}if(denied!==2)process.exit(8);`;
  const run=spawnSync(process.execPath,[new URL('./launch.mjs',import.meta.url).pathname,JSON.stringify(profile),'--',process.execPath,'-e',code],{encoding:'utf8',timeout:20000});
  assert.equal(run.status,0,run.stderr);assert.equal(await readFile(join(cwd,'allowed','good'),'utf8'),'yes');await assert.rejects(readFile(join(outside,'bad')));
 }finally{await rm(root,{recursive:true,force:true});}
});

test('explicit credential file is copied into persistent isolated provider runtime',async()=>{
 const root=await mkdtemp(join(tmpdir(),'whirlwind-auth-'));
 try{
  const cwd=join(root,'source'),runtime=join(root,'runtime'),authdir=join(root,'.codex');for(const path of [cwd,runtime,authdir])await mkdir(path);
  const auth=join(authdir,'auth.json');await writeFile(auth,'fixture-credential');
  const profile={cwd,runtime_dir:runtime,read_only:true,read_roots:[auth],domains:[]};
  const code=`const fs=require('fs'),path=require('path');const auth=path.join(process.env.CODEX_HOME,'auth.json');if(fs.readFileSync(auth,'utf8')!=='fixture-credential')process.exit(8);fs.writeFileSync(path.join(process.env.HOME,'session-state'),'persisted');`;
  const run=spawnSync(process.execPath,[new URL('./launch.mjs',import.meta.url).pathname,JSON.stringify(profile),'--',process.execPath,'-e',code],{encoding:'utf8',timeout:20000});
  assert.equal(run.status,0,run.stderr);assert.equal(await readFile(join(runtime,'home','session-state'),'utf8'),'persisted');assert.equal(await readFile(auth,'utf8'),'fixture-credential');
  await writeFile(join(runtime,'codex','auth.json'),'runtime-refreshed');
  const again=spawnSync(process.execPath,[new URL('./launch.mjs',import.meta.url).pathname,JSON.stringify(profile),'--',process.execPath,'-e',`if(require('fs').readFileSync(require('path').join(process.env.CODEX_HOME,'auth.json'),'utf8')!=='runtime-refreshed')process.exit(9)`],{encoding:'utf8',timeout:20000});
  assert.equal(again.status,0,again.stderr);
 }finally{await rm(root,{recursive:true,force:true});}
});

test('real reviewer sandbox prevents source mutation even through subprocess shell', async () => {
 const root=await mkdtemp(join(tmpdir(),'whirlwind-review-boundary-'));
 try {
  const file=join(root,'source');await writeFile(file,'original');
  const profile={cwd:root,read_only:true,policy_hash:'fixture',read_roots:[],domains:[],environment:[]};
  const run=spawnSync(process.execPath,[new URL('./launch.mjs',import.meta.url).pathname,JSON.stringify(profile),'--','/bin/sh','-c','printf changed > source'],{cwd:root,encoding:'utf8',timeout:20000});
  assert.notEqual(run.status,0);
  assert.equal(await readFile(file,'utf8'),'original');
  assert.match(run.stderr,/denied|permitted|read-only/i);
 } finally {await rm(root,{recursive:true,force:true});}
});

test('persisted runtime auth symlink cannot make host bootstrap overwrite an outside file',async()=>{
 const root=await mkdtemp(join(tmpdir(),'whirlwind-runtime-link-'));
 try{
  const cwd=join(root,'source'),runtime=join(root,'runtime'),authdir=join(root,'.codex');for(const path of [cwd,authdir,join(runtime,'codex')])await mkdir(path,{recursive:true});
  const outside=join(root,'outside');await writeFile(outside,'must-survive');
  const {utimes,symlink}=await import('node:fs/promises');await utimes(outside,1,1);
  const auth=join(authdir,'auth.json');await writeFile(auth,'credential');await symlink(outside,join(runtime,'codex','auth.json'));
  const profile={cwd,runtime_dir:runtime,read_only:true,read_roots:[auth],domains:[]};
  const run=spawnSync(process.execPath,[new URL('./launch.mjs',import.meta.url).pathname,JSON.stringify(profile),'--','/bin/true'],{encoding:'utf8',timeout:20000});
  assert.equal(await readFile(outside,'utf8'),'must-survive');
  assert.notEqual(run.status,0);
 }finally{await rm(root,{recursive:true,force:true});}
});
