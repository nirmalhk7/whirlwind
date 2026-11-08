import { SandboxManager } from '@anthropic-ai/sandbox-runtime';
import { spawn } from 'node:child_process';
import { mkdtemp, realpath, rm, lstat } from 'node:fs/promises';
import { existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { delimiter, dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

// The host supplies a structured profile and an argv array. No model-provided shell interpolation.
const quote = value => `'${String(value).replaceAll("'", "'\\''")}'`;
const literal = path => ({path,literal:true});
let scratch, temporary=false;
try {
 if (!['darwin','linux'].includes(process.platform)) throw new Error('Unsupported sandbox platform');
 const profile=JSON.parse(process.argv[2]);
 const command=process.argv.slice(4);
 if(process.argv[3]!=='--'||!command.length||!profile.cwd||typeof profile.read_only!=='boolean')throw new Error('Execution profile and argv required');
 const cwd=await realpath(profile.cwd);
 const node=await realpath(process.execPath);
 const runtimePath=dirname(node)+(process.env.PATH?delimiter+process.env.PATH:'');
 const bridge=await realpath(dirname(fileURLToPath(import.meta.url)));
 if(profile.runtime_dir){if((await lstat(profile.runtime_dir)).isSymbolicLink())throw new Error('Runtime root must not be a symbolic link');scratch=await realpath(profile.runtime_dir);}else{scratch=await mkdtemp(join(tmpdir(),'whirlwind-sandbox-'));temporary=true;}
 const homes={HOME:join(scratch,'home'),TMPDIR:join(scratch,'tmp'),XDG_CACHE_HOME:join(scratch,'cache'),XDG_CONFIG_HOME:join(scratch,'config'),CODEX_HOME:join(scratch,'codex'),CLAUDE_CONFIG_DIR:join(scratch,'claude')};
 const system=['/bin','/sbin','/usr','/lib','/lib64','/System','/Library/Developer/CommandLineTools','/opt/homebrew/Cellar','/opt/homebrew/opt','/usr/local/Cellar','/usr/local/opt','/private/etc/ssl/cert.pem','/etc/ssl/certs','/etc/ld.so.cache','/etc/resolv.conf','/etc/hosts','/etc/nsswitch.conf','/etc/passwd','/etc/group','/etc/protocols','/etc/services','/etc/localtime','/dev','/private/var/db/dyld','/private/var/select'].filter(existsSync);
 // Match only the OS alias symlink inode, not its descendants. Without these,
 // libc cannot traverse /etc to an explicitly allowed public runtime file.
 const aliases=process.platform==='darwin'?['/[e]tc','/[v]ar','/[t]mp']:[];
 const allowRead=[...[...system,cwd,scratch,bridge,...(profile.read_roots??[])].map(literal),...aliases];
 const denyWrite=[bridge,join(cwd,'.git'),join(cwd,'.whirlwind.yml'),join(cwd,'.whirlwind.yaml')].map(literal);
 // The wrapper and dependency package must stay immutable even when orchestrating its own repository.
 const writes=(profile.read_only?[]:(profile.allowed_paths??[]).map(path=>path==='**'?literal(cwd):join(cwd,path)));
 const config={filesystem:{denyRead:[literal('/')],allowRead,allowWrite:[literal(scratch),...writes,...(profile.write_roots??[]).map(literal)],denyWrite},network:{allowedDomains:profile.domains??[],deniedDomains:[],strictAllowlist:true,allowUnixSockets:profile.sockets??[],allowLocalBinding:false},enableWeakerNestedSandbox:false,enableWeakerNetworkIsolation:false};
 const deps=SandboxManager.checkDependencies();
 if(deps.errors?.length)throw new Error(`Sandbox dependencies unavailable: ${deps.errors.join('; ')}`);
 await SandboxManager.initialize(config,undefined,false);
 if(!SandboxManager.isSandboxingEnabled())throw new Error('Sandbox enforcement unavailable');
 const bootstrap=[node,join(bridge,'initialize-state.mjs'),JSON.stringify({homes,readRoots:profile.read_roots??[],nativeFiles:profile.native_files??[]}),'--',...command];
 const wrapped=await SandboxManager.wrapWithSandbox(bootstrap.map(quote).join(' '),'/bin/sh');
 const child=spawn(wrapped,{cwd,shell:'/bin/sh',stdio:'inherit',env:{...process.env,...homes,PATH:runtimePath,PWD:cwd,OPENSSL_CONF:'/dev/null'}});
 for(const signal of ['SIGTERM','SIGINT'])process.on(signal,()=>child.kill(signal));
 const code=await new Promise((resolve,reject)=>{child.once('error',reject);child.once('exit',(code,signal)=>resolve(code??(signal?128:1)));});
 process.exitCode=code;
}catch(error){console.error(`Sandbox setup/execution failed: ${error.message}`);process.exitCode=1;}
finally{await SandboxManager.reset();if(temporary&&scratch)await rm(scratch,{recursive:true,force:true});}
