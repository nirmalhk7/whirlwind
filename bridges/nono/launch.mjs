import {spawn,spawnSync} from 'node:child_process';
import {mkdtemp,realpath,lstat,writeFile,rm} from 'node:fs/promises';
import {existsSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {dirname,join,resolve,relative,isAbsolute,delimiter} from 'node:path';
import {fileURLToPath} from 'node:url';
import {createEgressProxy} from '../openshell/proxy.mjs';
import {randomBytes} from 'node:crypto';
import {codingRules} from './scope.mjs';

const bridge=dirname(fileURLToPath(import.meta.url));
const inside=(root,path)=>{const rel=relative(root,path);return rel!== '..'&&!rel.startsWith(`..${process.platform==='win32'?'\\':'/'}`)&&!isAbsolute(rel);};
let scratch,temporary=false,control,proxy;
try{
 const profile=JSON.parse(process.argv[2]);
 const command=process.argv.slice(4);
 if(process.argv[3]!=='--'||!command.length||!profile?.cwd||typeof profile.read_only!=='boolean')throw new Error('Execution profile and argv required');
 if(!['darwin','linux'].includes(process.platform))throw new Error('Unsupported sandbox platform');
 const cwd=await realpath(profile.cwd);
 const nono=process.env.WHIRLWIND_NONO_CLI||'nono';
 const version=spawnSync(nono,['--version'],{encoding:'utf8',timeout:5000});
 if(version.status!==0||version.stdout.trim()!=='nono 0.81.0')throw new Error('Install required nono 0.81.0');
 if(profile.runtime_dir){if((await lstat(profile.runtime_dir)).isSymbolicLink())throw new Error('Runtime root must not be a symbolic link');scratch=await realpath(profile.runtime_dir);}
 else{scratch=await mkdtemp(join(tmpdir(),'whirlwind-nono-state-'));temporary=true;}
 control=await mkdtemp(join(tmpdir(),'whirlwind-nono-control-'));
 const homes={HOME:join(scratch,'home'),TMPDIR:join(scratch,'tmp'),XDG_CACHE_HOME:join(scratch,'cache'),XDG_CONFIG_HOME:join(scratch,'config'),XDG_STATE_HOME:join(scratch,'state'),XDG_DATA_HOME:join(scratch,'data'),CODEX_HOME:join(scratch,'codex'),CLAUDE_CONFIG_DIR:join(scratch,'claude')};
 const token=process.env.WHIRLWIND_NONO_PROXY_TOKEN||randomBytes(32).toString('hex');
 proxy=createEgressProxy({token,allowedPrivateHosts:profile.domains??[],mcpBroker:profile.mcp_broker,mcpBrokerToken:process.env.WHIRLWIND_MCP_BROKER_TOKEN});
 await new Promise((ok,bad)=>{proxy.once('error',bad);proxy.listen(0,'127.0.0.1',ok);});
 const port=proxy.address().port;
 const system=['/bin','/sbin','/usr','/lib','/lib64','/System','/Library/Developer/CommandLineTools','/opt/homebrew/Cellar','/opt/homebrew/opt','/usr/local/Cellar','/usr/local/opt','/private/etc/ssl','/etc/ssl/certs','/etc/ld.so.cache','/etc/resolv.conf','/etc/hosts','/etc/nsswitch.conf','/etc/passwd','/etc/group','/etc/protocols','/etc/services','/etc/localtime','/private/var/db/dyld','/private/var/select','/dev'].filter(existsSync);
 const reads=[...system,cwd,bridge,resolve(bridge,'../sandbox/initialize-state.mjs'),resolve(bridge,'../openshell/mcp-stdio.mjs'),await realpath(process.execPath),...(profile.read_roots??[])];
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
 const policy={meta:{name:'whirlwind',version:'1.0.0'},groups:{include:[],exclude:['system_read_macos','system_read_linux_core','system_write_macos','system_write_linux','user_tools','homebrew_macos','homebrew_linux']},workdir:{access:'read'},filesystem,network:{block:true,open_port:[port]},security:{capability_elevation:false}};
 if(process.platform==='darwin')policy.unsafe_macos_seatbelt_rules=[...codingRules(profile,cwd),...protectedPaths.map(path=>`(deny file-write* (subpath ${JSON.stringify(path)}))`)];
 const policyFile=join(control,'profile.json');await writeFile(policyFile,JSON.stringify(policy),{mode:0o600,flag:'wx'});
 const proxyURL=`http://whirlwind:${token}@127.0.0.1:${port}`;
 const env={...process.env,XDG_CONFIG_HOME:join(control,'config'),XDG_STATE_HOME:join(control,'state'),XDG_DATA_HOME:join(control,'data'),PATH:dirname(await realpath(process.execPath))+delimiter+(process.env.PATH??''),PWD:cwd,OPENSSL_CONF:'/dev/null',SSL_CERT_FILE:process.platform==='darwin'?'/private/etc/ssl/cert.pem':'/etc/ssl/certs/ca-certificates.crt',HTTP_PROXY:proxyURL,HTTPS_PROXY:proxyURL,http_proxy:proxyURL,https_proxy:proxyURL,NO_PROXY:'',no_proxy:''};
 delete env.WHIRLWIND_MCP_BROKER_TOKEN;delete env.WHIRLWIND_NONO_PROXY_TOKEN;delete env.WHIRLWIND_NONO_CLI;
 for(const name of Object.keys(env))if(name.startsWith('NONO_'))delete env[name];
 const args=['wrap','--silent','--no-diagnostics','--profile',policyFile,'--allow-cwd','--',process.execPath,resolve(bridge,'../sandbox/initialize-state.mjs'),JSON.stringify({homes,readRoots:profile.read_roots??[],nativeFiles:profile.native_files??[]}),'--',...command];
 const child=spawn(nono,args,{cwd,stdio:'inherit',env});
 for(const signal of ['SIGINT','SIGTERM'])process.once(signal,()=>child.kill(signal));
 process.exitCode=await new Promise((ok,bad)=>{child.once('error',bad);child.once('exit',(code,signal)=>ok(code??(signal?128:1)));});
}catch(error){console.error(`nono execution failed: ${error.message}`);process.exitCode=1;}
finally{
 if(proxy)await new Promise(ok=>{proxy.close(()=>ok());proxy.closeAllConnections();});
 if(control)await rm(control,{recursive:true,force:true});
 if(temporary&&scratch)await rm(scratch,{recursive:true,force:true});
}
