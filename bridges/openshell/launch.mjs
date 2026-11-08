import {spawn,spawnSync} from 'node:child_process';
import {mkdtemp,writeFile,rm,lstat,realpath,open,rename,mkdir} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join,resolve,dirname,isAbsolute,sep} from 'node:path';
import {createHash,randomBytes} from 'node:crypto';
import {createEgressProxy} from './proxy.mjs';
import {buildDriverMounts,buildSandboxPolicy,requirePinnedGateway,workloadEnvironment} from './policy.mjs';

const fail=message=>{throw new Error(message);};
const runJson=(binary,args,label,timeout=10000,env=process.env)=>{
 const result=spawnSync(binary,args,{encoding:'utf8',env,timeout});
 if(result.error||result.status!==0) fail(`${label} failed${result.error?`: ${result.error.message}`:''}${result.stderr?`: ${result.stderr.trim()}`:''}`);
 try{return JSON.parse(result.stdout);}catch{fail(`${label} returned invalid structured output`);}
};
const runSuccessful=(binary,args,label,timeout=10000,env=process.env)=>{
 const result=spawnSync(binary,args,{encoding:'utf8',env,timeout});
 if(result.error||result.status!==0) fail(`${label} failed${result.error?`: ${result.error.message}`:''}${result.stderr?`: ${result.stderr.trim()}`:''}`);
 return result.stdout??'';
};
const shellQuote=value=>`'${String(value).replaceAll("'","'\\''")}'`;
function environmentFile(environment) {
 return Object.entries(environment).sort(([left],[right])=>left.localeCompare(right)).map(([name,value])=>{
  if(!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)||String(value).includes('\0')) fail('Unsafe sandbox environment entry');
  return `export ${name}=${shellQuote(value)}\n`;
 }).join('');
}

function nativeHomeFiles(profile, homeRoot) {
 const homes={HOME:homeRoot,CODEX_HOME:join(homeRoot,'codex'),CLAUDE_CONFIG_DIR:join(homeRoot,'claude')};
 const base=`/tmp/ww-native-${profile.execution_id}`;
 return (profile.native_files??[]).map(file=>{
  if(!homes[file.home]||typeof file.path!=='string'||!file.path||isAbsolute(file.path)||file.path.split(/[\\/]/).includes('..')) fail('Unsafe selected native credential path');
  const source=join(base,file.home,file.path);
  const target=join(homes[file.home],file.path);
  return `mkdir -p ${shellQuote(dirname(target))} && cp ${shellQuote(source)} ${shellQuote(target)} && chmod 600 ${shellQuote(target)} || exit\n`;
 }).join('');
}

async function execWithInput(openshell,args,input,cliEnv,deadline,label) {
 const child=spawn(openshell,args,{stdio:['pipe','inherit','inherit'],env:cliEnv});
 const remaining=remainingMs(deadline);
 const deadlineTimer=setTimeout(()=>child.kill('SIGTERM'),remaining);
 const code=await new Promise((resolve,reject)=>{
  child.once('error',reject);
  child.once('exit',(value,signal)=>resolve(value??(signal?128:1)));
  child.stdin.once('error',reject);
  child.stdin.end(input);
 }).finally(()=>clearTimeout(deadlineTimer));
 if(code!==0) fail(`${label} failed before the original attempt deadline`);
}

function remainingMs(deadline) {
 const remaining=Date.parse(deadline)-Date.now();
 if(!Number.isFinite(remaining)||remaining<=0) fail('OpenShell attempt deadline has expired');
 return remaining;
}

function authorityArgs(profile) {
 return ['--gateway',profile.gateway_name,'--gateway-endpoint',profile.gateway_endpoint,'--workspace',profile.workspace];
}

function validatePinnedProfile(profile) {
 for(const field of ['execution_id','sandbox_name','lifecycle_file','gateway_name','gateway_endpoint','workspace','execution_label_value','deadline_at'])
  if(typeof profile[field]!=='string'||!profile[field]) fail(`Persisted OpenShell ${field} is required`);
 if(!isAbsolute(profile.lifecycle_file)||!isAbsolute(profile.cwd)) fail('OpenShell lifecycle and worktree paths must be absolute');
 if(!Number.isSafeInteger(profile.execution_generation)||profile.execution_generation<1) fail('OpenShell execution generation must be positive');
 const identityHash=createHash('sha256').update(profile.execution_id).digest('hex').slice(0,24);
 if(profile.sandbox_name!==`ww-${identityHash.slice(0,16)}`||profile.execution_label_value!==`${identityHash}-${profile.execution_generation}`) fail('OpenShell name and label must be derived from the persisted execution generation');
 const lifecycle=resolve(profile.lifecycle_file);
 if(lifecycle===resolve(profile.cwd)||lifecycle.startsWith(`${resolve(profile.cwd)}${sep}`)) fail('Lifecycle proof must remain outside the sandbox worktree');
 remainingMs(profile.deadline_at);
}

function validateSandbox(sandbox,profile,expectedId='') {
 if(!sandbox||typeof sandbox!=='object'||sandbox.name!==profile.sandbox_name||sandbox.workspace!==profile.workspace||typeof sandbox.id!=='string'||!sandbox.id||(expectedId&&sandbox.id!==expectedId)||sandbox.labels?.['whirlwind.io/execution']!==profile.execution_label_value)
  fail('OpenShell sandbox identity does not match the persisted execution');
}

async function writeLifecycle(profile,sandbox,healthOverride='') {
 const parent=dirname(profile.lifecycle_file);
 await mkdir(parent,{recursive:true,mode:0o700});
 const parentStat=await lstat(parent);
 const resolvedParent=await realpath(parent);
 if(!parentStat.isDirectory()||parentStat.isSymbolicLink()||resolvedParent!==resolve(parent)||(parentStat.mode&0o077)!==0) fail('OpenShell lifecycle directory must be a private real supervisor directory');
 const temporary=`${profile.lifecycle_file}.${process.pid}.${randomBytes(6).toString('hex')}.tmp`;
 const file=await open(temporary,'wx',0o600);
 try { await file.writeFile(JSON.stringify({updated_at:new Date().toISOString(),execution_id:profile.execution_id,execution_generation:profile.execution_generation,
  deadline_at:profile.deadline_at,execution_label_value:profile.execution_label_value,
  sandbox_name:profile.sandbox_name,sandbox_id:sandbox.id,gateway_name:profile.gateway_name,gateway_endpoint:profile.gateway_endpoint,
  workspace:profile.workspace,sandbox_phase:sandbox.phase??'unknown',sandbox_health:healthOverride||(sandbox.phase==='Ready'?'healthy':'unhealthy')})); await file.sync(); }
 finally { await file.close(); }
 await rename(temporary,profile.lifecycle_file);
 const directory=await open(parent,'r');
 try { await directory.sync(); } finally { await directory.close(); }
}

function validatePinnedSandbox(sandbox,profile,expectedId='') {
 validateSandbox(sandbox,profile,expectedId);
}

function sandboxGetArgs(profile) {
 return ['sandbox','get',profile.sandbox_name,'--output','json',...authorityArgs(profile)];
}

async function probeSandbox(openshell,profile,expectedId) {
 const state=runJson(openshell,sandboxGetArgs(profile),'OpenShell sandbox health probe',Math.min(5000,remainingMs(profile.deadline_at)));
 validatePinnedSandbox(state,profile,expectedId);
 await writeLifecycle(profile,state);
}

function listSandboxes(openshell,profile,timeout=5000) {
 const sandboxes=[];
 let pageToken='';
 do {
  const args=['sandbox','list','--output','json',...authorityArgs(profile)];
  if(pageToken) args.push('--page-token',pageToken);
  const page=runJson(openshell,args,'OpenShell sandbox reconciliation',timeout);
  if(!page||!Array.isArray(page.sandboxes)) fail('OpenShell sandbox list returned an invalid envelope');
  sandboxes.push(...page.sandboxes);
  pageToken=page.next_page_token??'';
 } while(pageToken);
 return sandboxes;
}

async function stopAndDelete(openshell,profile,expectedId) {
 const authority=authorityArgs(profile);
 await writeLifecycle(profile,{id:expectedId,phase:'Stopping'},'stopping');
 let stopError;
 try{runSuccessful(openshell,['sandbox','stop',profile.sandbox_name,...authority],'OpenShell sandbox stop',15000);}
 catch(error){stopError=error;}
 const stopped=runJson(openshell,['sandbox','get',profile.sandbox_name,'--output','json',...authority],'OpenShell sandbox stop verification',15000);
 validatePinnedSandbox(stopped,profile,expectedId);
 if(stopped.phase!=='Stopped'&&!((stopped.phase==='Completed'||stopped.phase==='Error')&&Number.isInteger(stopped.exit_code))) {
  if(stopError) throw new AggregateError([stopError,new Error('OpenShell did not confirm sandbox isolation')],'OpenShell stop failed and isolation was not confirmed');
  fail('OpenShell did not confirm sandbox isolation; refusing successor execution');
 }
 await writeLifecycle(profile,stopped,'stopped');
 runSuccessful(openshell,['sandbox','delete',profile.sandbox_name,...authority],'OpenShell sandbox deletion',15000);
 const until=Date.now()+15000;
 while(Date.now()<until) {
  const remaining=listSandboxes(openshell,profile,5000).find(item=>item?.name===profile.sandbox_name);
  if(!remaining) return;
  validatePinnedSandbox(remaining,profile,expectedId);
  await new Promise(resolve=>setTimeout(resolve,250));
 }
 fail('OpenShell sandbox deletion did not complete; refusing successor execution');
}

async function main(){
 if(!['darwin','linux'].includes(process.platform)) fail('NVIDIA OpenShell execution requires a local Docker or Podman gateway host');
 const profile=JSON.parse(process.argv[2]??'null');
 const command=process.argv.slice(4);
 if(process.argv[3]!=='--'||!profile?.cwd||typeof profile.read_only!=='boolean'||!command.length) fail('Execution profile and command are required');
 validatePinnedProfile(profile);
 const image=process.env.WHIRLWIND_OPENSHELL_IMAGE?.trim();
 if(!image) fail('Set WHIRLWIND_OPENSHELL_IMAGE to a Linux image containing this ACP agent and its runtime');
 const cwd=await realpath(profile.cwd);
 if(cwd!==resolve(profile.cwd)) fail('Worktree path must be canonical before OpenShell can mount it');
 for(const file of profile.native_files??[]){
  const stat=await lstat(file.source).catch(()=>undefined);
  if(!stat?.isFile()||stat.isSymbolicLink()) fail('Scoped native credential must be a regular file');
 }
 const openshell=process.env.WHIRLWIND_OPENSHELL_CLI||'openshell';
 const gateways=runJson(openshell,['gateway','list','--output','json'],'OpenShell gateway discovery',Math.min(10000,remainingMs(profile.deadline_at)));
 const gateway=requirePinnedGateway(gateways,profile.gateway_name,profile.gateway_endpoint);
 const pinnedEnvironment={...process.env,OPENSHELL_GATEWAY:profile.gateway_name,OPENSHELL_GATEWAY_ENDPOINT:profile.gateway_endpoint,OPENSHELL_WORKSPACE:profile.workspace};
 const status=runJson(openshell,['status','--output','json'],'OpenShell gateway health check',10000,pinnedEnvironment);
 if(status.status!=='connected') fail('OpenShell gateway is unhealthy or unauthenticated; check `openshell status`');
 const info=runJson(openshell,['gateway','info','--output','json'],'OpenShell driver capability check',10000,pinnedEnvironment);
 const drivers=(info.compute_drivers??[]).map(driver=>driver.name).filter(name=>name==='docker'||name==='podman');
 if(!drivers.length) fail('OpenShell gateway must use a local Docker or Podman driver with bind mounts enabled');
 const proxyToken=process.env.WHIRLWIND_OPENSHELL_PROXY_TOKEN;
 if(!/^[a-f0-9]{64}$/.test(proxyToken??'')) fail('OpenShell proxy capability missing; restart the Whirlwind supervisor');
 const proxy=createEgressProxy({token:proxyToken,allowedPrivateHosts:profile.domains??[],mcpBroker:profile.mcp_broker,mcpBrokerToken:process.env.WHIRLWIND_MCP_BROKER_TOKEN});
 let sandboxId='';
 let work;
 let probeTimer;
 let probeInFlight=false;
 let createStarted=false;
 let executionError;
 let cleanupError;
 try{
  await new Promise((ok,bad)=>{proxy.once('error',bad);proxy.listen(0,'127.0.0.1',ok);});
  const address=proxy.address();
  if(!address||typeof address==='string') fail('Could not start the local OpenShell egress broker');
  // Every sandbox executable may use the local broker; the broker itself limits
  // destinations and rejects every port outside the explicit web allowlist.
  const policy=buildSandboxPolicy({...profile,cwd},address.port,['/**']);
  const mounts=buildDriverMounts({...profile,cwd});
  const driverConfig=Object.fromEntries(drivers.map(driver=>[driver,{mounts}]));
  work=await mkdtemp(join(tmpdir(),'whirlwind-openshell-'));
  const policyPath=join(work,'policy.json');
  await writeFile(policyPath,JSON.stringify(policy),{mode:0o600,flag:'wx'});
  const proxyURL=`http://whirlwind:${proxyToken}@host.openshell.internal:${address.port}`;
 const args=['sandbox','create','--name',profile.sandbox_name,'--from',image,'--policy',policyPath,'--driver-config-json',JSON.stringify(driverConfig),
  '--label',`whirlwind.io/execution=${profile.execution_label_value}`,'--detach','--no-auto-providers','--output','json',...authorityArgs(profile)];
  const workloadEnv=workloadEnvironment(profile,process.env);
  args.push('--','/bin/sh','-c','exec sleep infinity');
  const cliEnv={PATH:process.env.PATH??'',LANG:process.env.LANG??'C',LC_ALL:process.env.LC_ALL??'C',TERM:process.env.TERM??'',TMPDIR:process.env.TMPDIR??tmpdir(),OPENSHELL_GATEWAY:profile.gateway_name,OPENSHELL_GATEWAY_ENDPOINT:profile.gateway_endpoint,OPENSHELL_WORKSPACE:profile.workspace};
 if(process.env.XDG_CONFIG_HOME) cliEnv.XDG_CONFIG_HOME=process.env.XDG_CONFIG_HOME;
 if(process.env.XDG_STATE_HOME) cliEnv.XDG_STATE_HOME=process.env.XDG_STATE_HOME;
  createStarted=true;
  const created=runJson(openshell,args,'OpenShell sandbox creation',Math.min(10000,remainingMs(profile.deadline_at)),cliEnv);
  validatePinnedSandbox(created,profile);
  if(created.phase!=='Ready') fail('OpenShell sandbox did not become ready for its persisted execution');
  sandboxId=created.id;
  await writeLifecycle(profile,created);
  probeTimer=setInterval(()=>{
   if(probeInFlight)return;
   probeInFlight=true;
   probeSandbox(openshell,profile,sandboxId).catch(()=>{}).finally(()=>{probeInFlight=false;});
  },5000);
  const homeRoot=`/tmp/ww-${profile.execution_id}/home`;
  const tempRoot=`/tmp/ww-${profile.execution_id}/tmp`;
  const environment={HOME:homeRoot,CODEX_HOME:join(homeRoot,'codex'),CLAUDE_CONFIG_DIR:join(homeRoot,'claude'),TMPDIR:tempRoot,
   HTTP_PROXY:proxyURL,HTTPS_PROXY:proxyURL,http_proxy:proxyURL,https_proxy:proxyURL,NO_PROXY:'localhost,127.0.0.1,::1',no_proxy:'localhost,127.0.0.1,::1'};
  for(const name of workloadEnv) environment[name]=process.env[name];
  const envFile=`/tmp/ww-${profile.execution_id}.env`;
  const stageTimeout=Math.max(1,Math.ceil(remainingMs(profile.deadline_at)/1000));
  const stageArgs=['sandbox','exec','--name',profile.sandbox_name,...authorityArgs(profile),'--no-login-shell','--no-tty','--timeout',String(stageTimeout),'--workdir','/tmp','--','/bin/sh','-c','umask 077; mkdir -p "$2/codex" "$2/claude" "$3" && cat > "$1" && chmod 600 "$1"','whirlwind-env',envFile,homeRoot,tempRoot];
  await execWithInput(openshell,stageArgs,environmentFile(environment),cliEnv,profile.deadline_at,'OpenShell environment bootstrap');
  const timeout=Math.max(1,Math.ceil(remainingMs(profile.deadline_at)/1000));
  const executionScript=`set -a; . "$1" || exit; ${nativeHomeFiles(profile,homeRoot)}rm -f "$1"; shift; exec "$@"`;
  const execArgs=['sandbox','exec','--name',profile.sandbox_name,...authorityArgs(profile),'--no-login-shell','--no-tty','--timeout',String(timeout),'--workdir',cwd,'--','/bin/sh','-c',executionScript,'whirlwind-env',envFile,...command];
  const child=spawn(openshell,execArgs,{stdio:'inherit',env:cliEnv});
  const remaining=remainingMs(profile.deadline_at);
  const deadlineTimer=setTimeout(()=>child.kill('SIGTERM'),remaining);
  for(const signal of ['SIGTERM','SIGINT']) process.once(signal,()=>child.kill(signal));
  const code=await new Promise((ok,bad)=>{child.once('error',bad);child.once('exit',(value,signal)=>ok(value??(signal?128:1)));}).finally(()=>clearTimeout(deadlineTimer));
  process.exitCode=code;
 }catch(error){
  executionError=error;
 }finally{
  if(probeTimer) clearInterval(probeTimer);
  try {
   if(sandboxId) await stopAndDelete(openshell,profile,sandboxId);
   else if(createStarted) {
    const discovered=listSandboxes(openshell,profile,10000).find(item=>item?.name===profile.sandbox_name);
    if(discovered) {
     validatePinnedSandbox(discovered,profile);
     await stopAndDelete(openshell,profile,discovered.id);
    }
   }
  }catch(error){cleanupError=error;}
  try{await new Promise((ok,bad)=>{proxy.close(error=>error?bad(error):ok());});}
  catch(error){cleanupError=cleanupError?new AggregateError([cleanupError,error],'OpenShell cleanup failed'):error;}
  if(work) {
   try{await rm(work,{recursive:true,force:true});}
   catch(error){cleanupError=cleanupError?new AggregateError([cleanupError,error],'OpenShell cleanup failed'):error;}
  }
 }
 if(executionError&&cleanupError) throw new AggregateError([executionError,cleanupError],`OpenShell execution failed: ${executionError.message}; cleanup also failed: ${cleanupError.message}`);
 if(executionError) throw executionError;
 if(cleanupError) throw cleanupError;
}

main().catch(error=>{console.error(`NVIDIA OpenShell execution failed: ${error.message}`);process.exitCode=1;});
