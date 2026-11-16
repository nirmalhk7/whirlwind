import {spawn,spawnSync} from 'node:child_process';
import {mkdtemp,realpath,lstat,writeFile,rm,symlink} from 'node:fs/promises';
import {existsSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {basename,dirname,join,resolve,relative,isAbsolute,delimiter} from 'node:path';
import {fileURLToPath} from 'node:url';
import {createMCPGateway} from './mcp-gateway.mjs';
import {randomBytes} from 'node:crypto';
import {codingRules} from './scope.mjs';

const bridge=dirname(fileURLToPath(import.meta.url));
const inside=(root,path)=>{const rel=relative(root,path);return rel!== '..'&&!rel.startsWith(`..${process.platform==='win32'?'\\':'/'}`)&&!isAbsolute(rel);};
let scratch,temporary=false,control,proxy,cursorAlias;
try{
 const profile=JSON.parse(process.argv[2]);
 const command=process.argv.slice(4);
 if(process.argv[3]!=='--'||!command.length||!profile?.cwd||typeof profile.read_only!=='boolean')throw new Error('Execution profile and argv required');
 if(process.platform!=='darwin')throw new Error('Execution requires macOS; inspection remains available');
 const cwd=await realpath(profile.cwd);
 const nono=process.env.WHIRLWIND_NONO_CLI||'nono';
 const version=spawnSync(nono,['--version'],{encoding:'utf8',timeout:5000});
 if(version.status!==0||version.stdout.trim()!=='nono 0.81.0')throw new Error('Install required nono 0.81.0');
 if(profile.runtime_dir){if((await lstat(profile.runtime_dir)).isSymbolicLink())throw new Error('Runtime root must not be a symbolic link');scratch=await realpath(profile.runtime_dir);}
 else{scratch=await mkdtemp(join(tmpdir(),'whirlwind-nono-state-'));temporary=true;}
 control=await realpath(await mkdtemp(join(tmpdir(),'whirlwind-nono-control-')));
 const homes={HOME:join(scratch,'home'),TMPDIR:join(scratch,'tmp'),XDG_CACHE_HOME:join(scratch,'cache'),XDG_CONFIG_HOME:join(scratch,'config'),XDG_STATE_HOME:join(scratch,'state'),XDG_DATA_HOME:join(scratch,'data'),CODEX_HOME:join(scratch,'codex'),CLAUDE_CONFIG_DIR:join(scratch,'claude')};
 if(basename(command[0])==='cursor-agent'){
  // Cursor otherwise falls back to shared /tmp/.cursor for long state paths.
  // Only this trusted, non-writable alias is short; durable data stays in scratch.
  cursorAlias=await mkdtemp('/private/tmp/ww-cursor-');
  await symlink(scratch,join(cursorAlias,'runtime'),'dir');
  homes.CURSOR_DATA_DIR=join(cursorAlias,'runtime','data','cursor');
  homes.CURSOR_CONFIG_DIR=join(homes.HOME,'.cursor');
 }
 const token=process.env.WHIRLWIND_NONO_PROXY_TOKEN||randomBytes(32).toString('hex');
 let port;
 if(profile.mcp_broker){
  proxy=createMCPGateway({token,broker:profile.mcp_broker,brokerToken:process.env.WHIRLWIND_MCP_BROKER_TOKEN});
  await new Promise((ok,bad)=>{proxy.once('error',bad);proxy.listen(0,'127.0.0.1',ok);});
  port=proxy.address().port;
 }
 const credentials=profile.credentials??{};
 if(typeof credentials!=='object'||credentials===null||Array.isArray(credentials))throw new Error('Configure scoped credential routes');
 const services=Object.keys(credentials);
 if(services.length>32||services.some(service=>!/^[a-z][a-z0-9_]{0,31}$/.test(service)))throw new Error('Invalid credential service identity');
 const nativeCredentials=Object.fromEntries(services.map(service=>[service,{...credentials[service]}]));
 const credentialBindings=[],sourceNames=new Set(),targetNames=new Set();
 const reservedCredentialEnvironment=new Set(['PATH','HOME','CODEX_HOME','CLAUDE_CONFIG_DIR','PWD','TMPDIR','TMP','TEMP','OPENSSL_CONF','OPENSSL_MODULES','SSLKEYLOGFILE','NODE_OPTIONS','NODE_PATH','BASH_ENV','ENV','SHELLOPTS','PYTHONPATH','PYTHONSTARTUP','GIT_CONFIG','GIT_CONFIG_GLOBAL','GIT_CONFIG_SYSTEM','HTTP_PROXY','HTTPS_PROXY','ALL_PROXY','NO_PROXY','http_proxy','https_proxy','all_proxy','no_proxy','SSL_CERT_FILE','CURL_CA_BUNDLE','NODE_EXTRA_CA_CERTS','NODE_USE_ENV_PROXY']);
 const safeCredentialName=name=>typeof name==='string'&&/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)&&!reservedCredentialEnvironment.has(name)&&!/^(WHIRLWIND_|NONO_|OPENSHELL_|XDG_|DYLD_|LD_)/.test(name);
 for(const service of services){
  const credential=credentials[service],target=credential?.env_var;
  if(!safeCredentialName(target)||target===service.toUpperCase()+'_BASE_URL'||targetNames.has(target))throw new Error('Credential routes require distinct safe phantom environment variables');
  targetNames.add(target);
  if(typeof credential.credential_key!=='string')throw new Error('Credential source reference required');
  if(credential.credential_key.startsWith('env://')){
   const source=credential.credential_key.slice(6);
   if(!safeCredentialName(source)||!process.env[source])throw new Error('Credential source environment unavailable or unsafe');
   // nono replaces the target with a phantom token, but leaves differently
   // named source variables untouched. Resolve on the host and bind through
   // the target name so no original source value can reach the child.
   sourceNames.add(source);
   credentialBindings.push([target,process.env[source]]);
   nativeCredentials[service].credential_key='env://'+target;
  }
 }
 const domains=profile.domains??[];
 if(!Array.isArray(domains)||domains.some(domain=>typeof domain!=='string'||!domain||domain==='*'||domain==='**'||/[\s\x00]/.test(domain)))throw new Error('Configure explicit bounded network domains');
 const system=['/bin','/sbin','/usr','/lib','/lib64','/System','/Library/Developer/CommandLineTools','/opt/homebrew/Cellar','/opt/homebrew/opt','/usr/local/Cellar','/usr/local/opt','/private/etc/ssl','/etc/ssl/certs','/etc/ld.so.cache','/etc/resolv.conf','/etc/hosts','/etc/nsswitch.conf','/etc/passwd','/etc/group','/etc/protocols','/etc/services','/etc/localtime','/private/var/db/dyld','/private/var/select','/dev'].filter(existsSync);
 const reads=[...system,cwd,bridge,resolve(bridge,'../sandbox/initialize-state.mjs'),resolve(bridge,'../openshell/mcp-stdio.mjs'),await realpath(process.execPath),...(cursorAlias?[cursorAlias]:[]),...(profile.read_roots??[])];
 const writes=[scratch,...(profile.write_roots??[])];
 if(!profile.read_only&&process.platform!=='darwin'){
  for(const path of profile.allowed_paths??[]){
   if(path==='**'){writes.push(cwd);continue;}
   if(isAbsolute(path)||path.split(/[\\/]/).includes('..')||/[*?\[\]{}]/.test(path))throw new Error('nono coding grants require literal bounded paths or **');
   const target=await realpath(join(cwd,path));if(!inside(cwd,target))throw new Error('Coding path escapes worktree');writes.push(target);
  }
 }
 // Landlock cannot subtract write grants beneath a writable parent. Refuse
 // such a profile rather than weakening Git and policy protection on Linux.
 if(process.platform==='linux'&&writes.some(path=>inside(path,cwd)))throw new Error('nono Linux cannot protect metadata beneath a whole-worktree write grant; use bounded allowed_paths');
 const filesystem={read:[],read_file:[],allow:[],allow_file:[],unix_socket:profile.sockets??[]};
 for(const [paths,access] of [[reads,'read'],[writes,'allow']])for(const path of [...new Set(paths)]){
  const stat=await lstat(path);filesystem[stat.isDirectory()?access:`${access}_file`].push(path);
 }
 const protectedPaths=[join(cwd,'.git'),join(cwd,'.whirlwind.yml'),join(cwd,'.whirlwind.yaml'),bridge,resolve(bridge,'../sandbox')];
 const policy={meta:{name:'whirlwind',version:'1.0.0'},groups:{include:[],exclude:['system_read_macos','system_read_linux_core','system_write_macos','system_write_linux','user_tools','homebrew_macos','homebrew_linux']},workdir:{access:'read'},filesystem,network:{block:domains.length===0&&services.length===0,open_port:port?[port]:[],custom_credentials:nativeCredentials,credentials:services},security:{capability_elevation:false}};
 if(process.platform==='darwin'){
  const escapedControl=control.replace(/[.+^${}()|[\]\\]/g,'\\$&');
  const publicCA=`(allow file-read* (regex ${JSON.stringify(`^${escapedControl}/state/nono/sessions/intercept-[^/]+/intercept-ca\\.pem$`)}))`;
  policy.unsafe_macos_seatbelt_rules=['(allow file-write-data (literal "/dev/null"))',...codingRules(profile,cwd),...(domains.length||services.length?[publicCA]:[]),...protectedPaths.map(path=>`(deny file-write* (subpath ${JSON.stringify(path)}))`)];
 }
 const policyFile=join(control,'profile.json');await writeFile(policyFile,JSON.stringify(policy),{mode:0o600,flag:'wx'});
 const proxyURL=port?`http://whirlwind:${token}@127.0.0.1:${port}`:undefined;
 const env={...process.env,XDG_CONFIG_HOME:join(control,'config'),XDG_STATE_HOME:join(control,'state'),XDG_DATA_HOME:join(control,'data'),PATH:dirname(await realpath(process.execPath))+delimiter+(process.env.PATH??''),PWD:cwd,OPENSSL_CONF:'/dev/null',SSL_CERT_FILE:process.platform==='darwin'?'/private/etc/ssl/cert.pem':'/etc/ssl/certs/ca-certificates.crt',NO_PROXY:'',no_proxy:''};
 for(const name of ['HTTP_PROXY','HTTPS_PROXY','http_proxy','https_proxy','ALL_PROXY','all_proxy','WHIRLWIND_TOOL_PROXY'])delete env[name];
 for(const name of sourceNames)delete env[name];
 for(const [name,value] of credentialBindings)env[name]=value;
 if(proxyURL)env.WHIRLWIND_TOOL_PROXY=proxyURL;
 delete env.WHIRLWIND_MCP_BROKER_TOKEN;delete env.WHIRLWIND_NONO_PROXY_TOKEN;delete env.WHIRLWIND_NONO_CLI;
 for(const name of Object.keys(env))if(name.startsWith('NONO_'))delete env[name];
 const args=['run','--silent','--no-diagnostics','--no-rollback','--no-rollback-prompt','--profile',policyFile,'--allow-cwd',...domains.flatMap(domain=>['--allow-domain',domain]),'--',process.execPath,resolve(bridge,'../sandbox/initialize-state.mjs'),JSON.stringify({homes,readRoots:profile.read_roots??[],nativeFiles:profile.native_files??[]}),'--',...command];
 const child=spawn(nono,args,{cwd,stdio:'inherit',env});
 for(const signal of ['SIGINT','SIGTERM'])process.once(signal,()=>child.kill(signal));
 process.exitCode=await new Promise((ok,bad)=>{child.once('error',bad);child.once('exit',(code,signal)=>ok(code??(signal?128:1)));});
}catch(error){console.error(`nono execution failed: ${error.message}`);process.exitCode=1;}
finally{
 if(proxy)await new Promise(ok=>{proxy.close(()=>ok());proxy.closeAllConnections();});
 if(control)await rm(control,{recursive:true,force:true});
 if(temporary&&scratch)await rm(scratch,{recursive:true,force:true});
 if(cursorAlias)await rm(cursorAlias,{recursive:true,force:true});
}
