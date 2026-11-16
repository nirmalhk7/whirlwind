import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,writeFile,readFile,mkdir,rm,lstat} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import {spawn,spawnSync} from 'node:child_process';

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

// Shell launchers redirect diagnostics to this sink; other device files remain protected.
test('nono permits the null sink without opening other device writes',async()=>{
 const cwd=await mkdtemp(join(tmpdir(),'ww-nono-null-'));
 try{
  const profile={cwd,read_only:true,read_roots:[],domains:[],environment:[]};
  const code=`const fs=require('fs');fs.writeFileSync('/dev/null','discard');try{fs.openSync('/dev/zero','w');process.exit(39)}catch(e){if(!['EPERM','EACCES'].includes(e.code))throw e}console.log('null-ready');`;
  const result=spawnSync(process.execPath,[resolve('bridges/nono/launch.mjs'),JSON.stringify(profile),'--',process.execPath,'-e',code],{encoding:'utf8',timeout:20000});
  assert.equal(result.status,0,result.stderr);assert.equal(result.stdout.trim(),'null-ready');
 }finally{await rm(cwd,{recursive:true,force:true});}
});

// Cursor falls back to shared /tmp state when its data path exceeds its IPC limit.
test('nono gives Cursor short private data paths backed by persistent runtime state',async()=>{
 const root=await mkdtemp(join(tmpdir(),'ww-nono-cursor-'));
 try{
  const cwd=join(root,'repo'),runtime=join(root,'long-runtime-directory'.repeat(5));
  await mkdir(cwd);await mkdir(runtime);
  const launcher=join(root,'cursor-agent'),probe=join(root,'probe.js'),auth=join(root,'auth.json');
  await writeFile(auth,'selected-auth');
  await writeFile(launcher,`#!/bin/sh\nexec "${process.execPath}" "${probe}"\n`,{mode:0o700});
  await writeFile(probe,`const fs=require('fs'),path=require('path');const dir=process.env.CURSOR_DATA_DIR;if(!dir||path.join(dir,'projects').length>83)throw new Error('Cursor data path exceeds IPC limit');const config=process.env.CURSOR_CONFIG_DIR||path.join(process.env.XDG_CONFIG_HOME,'cursor');if(fs.readFileSync(path.join(config,'cli-config.json'),'utf8')!=='selected-auth')throw new Error('Selected Cursor credential unavailable');const alias=path.resolve(dir,'../../..');try{fs.writeFileSync(path.join(alias,'escaped'),'bad');process.exit(38)}catch(e){if(!['EPERM','EACCES'].includes(e.code))throw e}fs.writeFileSync(path.join(dir,'retained'),'private-state');console.log(JSON.stringify({state:'cursor-private',alias}));`);
  const profile={cwd,read_only:true,runtime_dir:runtime,read_roots:[launcher,probe,auth],native_files:[{source:auth,home:'HOME',path:'.cursor/cli-config.json'}],domains:[],environment:[]};
  const result=spawnSync(process.execPath,[resolve('bridges/nono/launch.mjs'),JSON.stringify(profile),'--',launcher],{encoding:'utf8',timeout:20000});
  assert.equal(result.status,0,result.stderr);const resultState=JSON.parse(result.stdout);assert.equal(resultState.state,'cursor-private');
  await assert.rejects(lstat(resultState.alias),{code:'ENOENT'});
  assert.equal(await readFile(join(runtime,'data','cursor','retained'),'utf8'),'private-state');
 }finally{await rm(root,{recursive:true,force:true});}
});

test('Linux inspection cannot launch execution or invoke native runtime',async()=>{
 const root=await mkdtemp(join(tmpdir(),'ww-linux-closed-'));
 try{
  const marker=join(root,'invoked'),fake=join(root,'nono');
  await writeFile(fake,`#!/bin/sh\nprintf invoked > '${marker}'\nprintf 'nono 0.81.0\\n'\n`,{mode:0o700});
  const profile={cwd:root,read_only:true,read_roots:[],write_roots:[],domains:[],environment:[]};
  const code=`Object.defineProperty(process,'platform',{value:'linux'});process.argv=[process.execPath,${JSON.stringify(resolve('bridges/nono/launch.mjs'))},${JSON.stringify(JSON.stringify(profile))},'--','/usr/bin/true'];await import(${JSON.stringify('file://'+resolve('bridges/nono/launch.mjs'))});`;
  const result=spawnSync(process.execPath,['--input-type=module','-e',code],{encoding:'utf8',timeout:10000,env:{...process.env,WHIRLWIND_NONO_CLI:fake}});
  assert.notEqual(result.status,0);
  await assert.rejects(readFile(marker),{code:'ENOENT'});
  assert.match(result.stderr,/macOS.*inspection/i);
 }finally{await rm(root,{recursive:true,force:true});}
});

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

// An explicit allowlist must block other domains before DNS/upstream access.
test('nono rejects unapproved domains through the child proxy',async()=>{
 const cwd=await mkdtemp(join(tmpdir(),'ww-nono-filter-'));
 try{
  const profile={cwd,read_only:true,read_roots:[],domains:['allowed.whirlwind.invalid'],environment:[]};
  const code=`const http=require('http');const proxy=new URL(process.env.HTTP_PROXY);const request=http.request({host:proxy.hostname,port:proxy.port,method:'CONNECT',path:'example.com:443',headers:{host:'example.com:443','proxy-authorization':'Basic '+Buffer.from(decodeURIComponent(proxy.username)+':'+decodeURIComponent(proxy.password)).toString('base64')}});request.on('connect',(response,socket)=>{socket.destroy();if(response.statusCode!==403){console.error('Unapproved destination returned '+response.statusCode);process.exit(34)}console.log('domain-denied')});request.on('error',e=>{console.error(e.message);process.exit(35)});request.setTimeout(5000,()=>request.destroy(new Error('unapproved destination did not fail closed')));request.end();`;
  const result=spawnSync(process.execPath,[resolve('bridges/nono/launch.mjs'),JSON.stringify(profile),'--',process.execPath,'-e',code],{encoding:'utf8',timeout:20000});
  assert.equal(result.status,0,result.stderr);assert.equal(result.stdout.trim(),'domain-denied');
 }finally{await rm(cwd,{recursive:true,force:true});}
});


// Internal tool transport must not reveal its real broker token or provide a
// second public egress path beside nono's filtered network proxy.
test('nono preserves scoped MCP access without a public proxy bypass',async()=>{
 const {createServer}=await import('node:http');
 const root=await mkdtemp(join(tmpdir(),'ww-nono-mcp-'));
 const cwd=join(root,'repo'),socket=join(root,'broker.sock'),secret='fixture-broker-secret-that-must-stay-on-host';
 await mkdir(cwd);
 let requests=0;
 const server=createServer((request,response)=>{
  assert.equal(request.url,'/mcp');assert.equal(request.method,'POST');assert.equal(request.headers['x-whirlwind-mcp-token'],secret);
  requests++;request.resume();request.on('end',()=>{response.setHeader('content-type','application/json');response.end(JSON.stringify({jsonrpc:'2.0',id:1,result:{tools:[]}}));});
 });
 await new Promise((ok,bad)=>{server.once('error',bad);server.listen(socket,ok)});
 try{
  const profile={cwd,read_only:true,read_roots:[],domains:[],environment:[],mcp_broker:{id:'scoped-tools',socket_path:socket,host:'whirlwind-mcp.openshell.internal',path:'/mcp'}};
  const shim=resolve('bridges/nono/mcp-stdio.mjs');
  const code=`if(process.env.WHIRLWIND_MCP_BROKER_TOKEN)process.exit(41);const http=require('http');const proxy=new URL(process.env.WHIRLWIND_TOOL_PROXY||process.env.HTTP_PROXY);const request=http.request({host:proxy.hostname,port:proxy.port,method:'CONNECT',path:'example.com:443',headers:{host:'example.com:443','proxy-authorization':'Basic '+Buffer.from(decodeURIComponent(proxy.username)+':'+decodeURIComponent(proxy.password)).toString('base64')}});request.on('connect',(response,socket)=>{socket.destroy();if(response.statusCode!==403){console.error('MCP proxy exposed public tunnel: '+response.statusCode);process.exit(42)}const child=require('child_process').spawn(process.execPath,[${JSON.stringify(shim)},'scoped-tools'],{stdio:'inherit'});child.on('exit',code=>process.exit(code??43));child.on('error',()=>process.exit(44));});request.on('error',e=>{console.error(e.message);process.exit(45)});request.end();`;
  const child=spawn(process.execPath,[resolve('bridges/nono/launch.mjs'),JSON.stringify(profile),'--',process.execPath,'-e',code],{env:{...process.env,WHIRLWIND_MCP_BROKER_TOKEN:secret},stdio:['pipe','pipe','pipe']});
  let stdout='',stderr='';child.stdout.on('data',data=>stdout+=data);child.stderr.on('data',data=>stderr+=data);
  const timer=setTimeout(()=>child.kill('SIGTERM'),15000);
  child.stdin.end(JSON.stringify({jsonrpc:'2.0',id:1,method:'tools/list'})+'\n');
  const status=await new Promise((ok,bad)=>{child.once('error',bad);child.once('exit',ok)});clearTimeout(timer);
  assert.equal(status,0,stderr);assert.equal(requests,1);assert.deepEqual(JSON.parse(stdout),{jsonrpc:'2.0',id:1,result:{tools:[]}});
 }finally{await new Promise(ok=>server.close(ok));await rm(root,{recursive:true,force:true})}
});

// Scoped HTTPS paths require native TLS inspection and child-only CA trust.
test('nono filters HTTPS paths using child scoped certificate trust',async()=>{
 const cwd=await mkdtemp(join(tmpdir(),'ww-nono-path-'));
 try{
  const profile={cwd,read_only:true,read_roots:[],domains:['https://example.com/allowed/**'],environment:[]};
  const code=`const http=require('http'),tls=require('tls'),fs=require('fs');const proxy=new URL(process.env.HTTPS_PROXY);const request=http.request({host:proxy.hostname,port:proxy.port,method:'CONNECT',path:'example.com:443',headers:{host:'example.com:443','proxy-authorization':'Basic '+Buffer.from(decodeURIComponent(proxy.username)+':'+decodeURIComponent(proxy.password)).toString('base64')}});request.on('connect',(response,socket)=>{if(response.statusCode!==200){console.error('Scoped TLS tunnel failed: '+response.statusCode);process.exit(46)}const secure=tls.connect({socket,servername:'example.com',ca:(()=>{const ca=fs.readFileSync(process.env.SSL_CERT_FILE);try{fs.readdirSync(require('path').dirname(process.env.SSL_CERT_FILE));process.exit(50)}catch(e){if(!['EPERM','EACCES'].includes(e.code))throw e}return ca})()});secure.once('secureConnect',()=>secure.write('GET /denied HTTP/1.1\\r\\nHost: example.com\\r\\nConnection: close\\r\\n\\r\\n'));let body='';secure.on('data',data=>body+=data);secure.on('end',()=>{if(!/^HTTP\\/1\\.[01] 403 /.test(body)){console.error('Unapproved HTTPS path returned '+body.slice(0,80));process.exit(47)}console.log('path-denied')});secure.on('error',e=>{console.error(e.message);process.exit(48)})});request.on('error',e=>{console.error(e.message);process.exit(49)});request.end();`;
  const result=spawnSync(process.execPath,[resolve('bridges/nono/launch.mjs'),JSON.stringify(profile),'--',process.execPath,'-e',code],{encoding:'utf8',timeout:20000});
  assert.equal(result.status,0,result.stderr);assert.equal(result.stdout.trim(),'path-denied');
 }finally{await rm(cwd,{recursive:true,force:true})}
});

// Real upstream credentials remain on the host; the child receives only the
// native proxy's scoped phantom token and cannot bypass method/path grants.
test('nono injects scoped credentials outside child and enforces endpoint methods',async()=>{
 const {createServer}=await import('node:http');
 const root=await mkdtemp(join(tmpdir(),'ww-nono-credential-'));
 const cwd=join(root,'repo'),keyFile=join(root,'private-key'),secret='fixture-upstream-secret-for-native-proxy';
 await mkdir(cwd);await writeFile(keyFile,secret,{mode:0o600});
 const received=[];
 const server=createServer((request,response)=>{
  received.push({method:request.method,path:request.url,key:request.headers['x-fixture-key']});
  request.resume();request.on('end',()=>{response.writeHead(201,{'content-type':'application/json'});response.end('{"ok":true}')});
 });
 await new Promise(ok=>server.listen(0,'127.0.0.1',ok));
 try{
  const profile={cwd,read_only:true,read_roots:[],domains:[],environment:[],credentials:{fixture:{upstream:`http://127.0.0.1:${server.address().port}`,credential_key:'file://'+keyFile,env_var:'FIXTURE_API_KEY',inject_header:'X-Fixture-Key',credential_format:'{}',endpoint_rules:[{method:'POST',path:'/allowed'}]}}};
  const code=`const http=require('http'),fs=require('fs');const key=process.env.FIXTURE_API_KEY;if(!key){console.error('credential route unavailable');process.exit(51)}try{fs.readFileSync(${JSON.stringify(keyFile)});process.exit(52)}catch(e){if(!['EPERM','EACCES'].includes(e.code))throw e}const base=process.env.FIXTURE_BASE_URL||new URL(process.env.HTTP_PROXY).origin+'/fixture';const call=(method,path)=>new Promise((ok,bad)=>{const request=http.request(base+path,{method,agent:false,headers:{'X-Fixture-Key':key}},response=>{response.resume();response.once('end',()=>ok(response.statusCode))});request.once('error',e=>bad(new Error(method+' '+path+': '+e.message+'; base='+new URL(base).origin+new URL(base).pathname+'; env='+Object.keys(process.env).filter(k=>/NONO|FIXTURE|PROXY/.test(k)).join(','))));request.end()});(async()=>{const deniedMethod=await call('GET','/allowed');const deniedPath=await call('POST','/denied');const allowed=await call('POST','/allowed');console.log(JSON.stringify({key,deniedMethod,deniedPath,allowed}))})().catch(e=>{console.error(e.message);process.exit(53)});`;
  const child=spawn(process.execPath,[resolve('bridges/nono/launch.mjs'),JSON.stringify(profile),'--',process.execPath,'-e',code],{stdio:['ignore','pipe','pipe']});
  let stdout='',stderr='';child.stdout.on('data',data=>stdout+=data);child.stderr.on('data',data=>stderr+=data);
  const timer=setTimeout(()=>child.kill('SIGTERM'),15000);
  const status=await new Promise((ok,bad)=>{child.once('error',bad);child.once('exit',ok)});clearTimeout(timer);
  assert.equal(status,0,stderr);const result=JSON.parse(stdout);assert.ok(result.key);assert.notEqual(result.key,secret);
  assert.equal(result.deniedMethod,403);assert.equal(result.deniedPath,403);assert.equal(result.allowed,201);
  assert.deepEqual(received,[{method:'POST',path:'/allowed',key:secret}]);assert.ok(!stdout.includes(secret)&&!stderr.includes(secret));
 }finally{await new Promise(ok=>server.close(ok));await rm(root,{recursive:true,force:true})}
});
