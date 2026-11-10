import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,writeFile,readFile,mkdir,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import {spawnSync} from 'node:child_process';

// A missing native boundary, broad worktree write grant, or ambient home grant breaks this test.
test('nono enforces read-only source, private home and writable declared output',async()=>{
 const root=await mkdtemp(join(tmpdir(),'ww-nono-test-'));
 try {
  const cwd=join(root,'repo'),output=join(cwd,'dist'),runtime=join(root,'runtime');
  await mkdir(output,{recursive:true}); await mkdir(runtime);
  const source=join(cwd,'source.txt'),outside=join(root,'secret.txt');
  await writeFile(source,'source'); await writeFile(outside,'secret');
  const profile={cwd,read_only:true,read_roots:[],write_roots:[output],runtime_dir:runtime,domains:[],environment:[]};
  const code=`const fs=require('fs');for(const [op,path] of [['readFileSync',${JSON.stringify(outside)}],['writeFileSync',${JSON.stringify(source)}]]){try{fs[op](path,op==='readFileSync'?'utf8':'changed');process.exit(30)}catch(e){if(!['EPERM','EACCES'].includes(e.code))throw e}}fs.writeFileSync(${JSON.stringify(join(output,'result'))},'ok');console.log('isolated');`;
  const result=spawnSync(process.execPath,[resolve('bridges/nono/launch.mjs'),JSON.stringify(profile),'--',process.execPath,'-e',code],{encoding:'utf8',timeout:20000});
  assert.equal(result.status,0,result.stderr); assert.equal(result.stdout.trim(),'isolated');
  assert.equal(await readFile(source,'utf8'),'source'); assert.equal(await readFile(join(output,'result'),'utf8'),'ok');
 }finally{await rm(root,{recursive:true,force:true});}
});

// Coding must not make repository metadata or policy writable.
test('nono permits coding while protecting Git metadata and project policy',async()=>{
 const root=await mkdtemp(join(tmpdir(),'ww-nono-code-'));
 try{
  const cwd=join(root,'repo');await mkdir(join(cwd,'.git'),{recursive:true});
  const policy=join(cwd,'.whirlwind.yaml');await writeFile(policy,'policy');
  const profile={cwd,read_only:false,allowed_paths:['**'],read_roots:[],write_roots:[],domains:[],environment:[]};
  const code=`const fs=require('fs');fs.writeFileSync('feature.txt','feature');for(const path of ['.git/config','.whirlwind.yaml']){try{fs.writeFileSync(path,'changed');process.exit(31)}catch(e){if(!['EPERM','EACCES'].includes(e.code))throw e}}`;
  const result=spawnSync(process.execPath,[resolve('bridges/nono/launch.mjs'),JSON.stringify(profile),'--',process.execPath,'-e',code],{encoding:'utf8',timeout:20000});
  assert.equal(result.status,0,result.stderr);assert.equal(await readFile(join(cwd,'feature.txt'),'utf8'),'feature');assert.equal(await readFile(policy,'utf8'),'policy');
 }finally{await rm(root,{recursive:true,force:true});}
});

// Selected credentials must be copied without opening the rest of the host home.
test('nono bootstraps only selected credential files',async()=>{
 const root=await mkdtemp(join(tmpdir(),'ww-nono-auth-'));
 try{
  const cwd=join(root,'repo'),home=join(root,'home'),auth=join(home,'.codex','auth.json');
  await mkdir(cwd);await mkdir(dirnameForAuth(auth),{recursive:true});await writeFile(auth,'selected-auth');
  const profile={cwd,read_only:true,read_roots:[auth],native_files:[{source:auth,home:'CODEX_HOME',path:'auth.json'}],environment:[],domains:[]};
  const result=spawnSync(process.execPath,[resolve('bridges/nono/launch.mjs'),JSON.stringify(profile),'--',process.execPath,'-e',"process.stdout.write(require('fs').readFileSync(process.env.CODEX_HOME+'/auth.json','utf8'))"],{encoding:'utf8',timeout:20000,env:{...process.env,HOME:home}});
  assert.equal(result.status,0,result.stderr);assert.equal(result.stdout,'selected-auth');
 }finally{await rm(root,{recursive:true,force:true});}
});
function dirnameForAuth(path){return resolve(path,'..');}

// Proxy variables alone are insufficient: direct loopback sockets must be kernel-denied.
test('nono denies direct network access outside its egress broker',async()=>{
 const {createServer}=await import('node:net');
 const server=createServer(socket=>socket.end('escaped'));
 await new Promise(ok=>server.listen(0,'127.0.0.1',ok));
 const cwd=await mkdtemp(join(tmpdir(),'ww-nono-network-'));
 try{
  const profile={cwd,read_only:true,read_roots:[],domains:[],environment:[]};
  const code=`const socket=require('net').connect(${server.address().port},'127.0.0.1');socket.on('connect',()=>process.exit(32));socket.on('error',e=>{if(!['EPERM','EACCES'].includes(e.code))throw e;console.log('network-denied')});`;
  const result=spawnSync(process.execPath,[resolve('bridges/nono/launch.mjs'),JSON.stringify(profile),'--',process.execPath,'-e',code],{encoding:'utf8',timeout:20000});
  assert.equal(result.status,0,result.stderr);assert.equal(result.stdout.trim(),'network-denied');
 }finally{await new Promise(ok=>server.close(ok));await rm(cwd,{recursive:true,force:true});}
});

// Native TLS tools need public CA/config files, without reading host credentials.
test('nono permits system TLS initialization',async()=>{
 if(process.platform!=='darwin')return;
 const cwd=await mkdtemp(join(tmpdir(),'ww-nono-tls-'));
 try{
  const profile={cwd,read_only:true,read_roots:[],domains:[],environment:[]};
  const result=spawnSync(process.execPath,[resolve('bridges/nono/launch.mjs'),JSON.stringify(profile),'--','/usr/bin/curl','--version'],{encoding:'utf8',timeout:20000});
  assert.equal(result.status,0,result.stderr);assert.match(result.stdout,/curl/);
 }finally{await rm(cwd,{recursive:true,force:true});}
});

// Existing project policies use recursive patterns and allow creation of new files.
test('nono coding honors recursive scope and new literal files',async()=>{
 const cwd=await mkdtemp(join(tmpdir(),'ww-nono-scope-'));
 try{
  await mkdir(join(cwd,'src'));
  const profile={cwd,read_only:false,allowed_paths:['src/**','NEW.md'],read_roots:[],domains:[],environment:[]};
  const code="const fs=require('fs');fs.writeFileSync('src/new.js','scoped');fs.writeFileSync('NEW.md','new');try{fs.writeFileSync('escaped.js','bad');process.exit(33)}catch(e){if(!['EPERM','EACCES'].includes(e.code))throw e}";
  const result=spawnSync(process.execPath,[resolve('bridges/nono/launch.mjs'),JSON.stringify(profile),'--',process.execPath,'-e',code],{encoding:'utf8',timeout:20000});
  assert.equal(result.status,0,result.stderr);assert.equal(await readFile(join(cwd,'src/new.js'),'utf8'),'scoped');
 }finally{await rm(cwd,{recursive:true,force:true});}
});
