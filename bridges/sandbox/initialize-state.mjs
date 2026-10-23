// This trusted bootstrap always runs INSIDE the enforced sandbox. Persistent
// state is untrusted: links planted by a previous process cannot widen writes.
import {mkdir,copyFile,chmod,stat,lstat} from 'node:fs/promises';
import {join,isAbsolute,relative,dirname} from 'node:path';
import {spawn} from 'node:child_process';

try{
 const {homes,readRoots,nativeFiles=[]}=JSON.parse(process.argv[2]);
 const command=process.argv.slice(4);if(process.argv[3]!=='--'||!command.length)throw new Error('Bootstrap argv required');
 for(const path of Object.values(homes)){
  const existing=await lstat(path).catch(()=>undefined);
  if(existing?.isSymbolicLink())throw new Error('Runtime state directory must not be a symbolic link');
  await mkdir(path,{recursive:true,mode:0o700});
 }
 for(const file of nativeFiles){
  const base=homes[file.home];
  if(!base||typeof file.path!=='string'||isAbsolute(file.path)||file.path.split(/[\\/]/).includes('..')||!file.path||!(readRoots??[]).includes(file.source))throw new Error('Invalid native integration file');
  const target=join(base,file.path),rel=relative(base,target);
  if(!rel||rel.startsWith('..')||isAbsolute(rel))throw new Error('Native integration destination escapes runtime');
  let parent=dirname(target);
  while(parent!==base){const existing=await lstat(parent).catch(()=>undefined);if(existing?.isSymbolicLink()||(existing&&!existing.isDirectory()))throw new Error('Native integration directory must not be a link');parent=dirname(parent);}
  await mkdir(dirname(target),{recursive:true,mode:0o700});
  const source=await lstat(file.source);if(!source.isFile()||source.isSymbolicLink())throw new Error('Native integration source must be a regular file');
  const existing=await lstat(target).catch(()=>undefined);if(existing&&(!existing.isFile()||existing.isSymbolicLink()))throw new Error('Native integration destination must be a regular file');
  const original=await stat(file.source);if(!existing||existing.mtimeMs<original.mtimeMs){await copyFile(file.source,target);await chmod(target,0o600);}
 }
 for(const source of readRoots??[]){
  let target;
  if(source.endsWith('/.codex/auth.json'))target=join(homes.CODEX_HOME,'auth.json');
  if(source.endsWith('/.claude/.credentials.json'))target=join(homes.CLAUDE_CONFIG_DIR,'.credentials.json');
  if(!target)continue;
  const entry=await lstat(target).catch(()=>undefined);
  if(entry?.isSymbolicLink())throw new Error('Runtime credential must not be a symbolic link');
  const current=await stat(target).catch(()=>undefined),original=await stat(source);
  if(!current||current.mtimeMs<original.mtimeMs){await copyFile(source,target);await chmod(target,0o600);}
 }
 const child=spawn(command[0],command.slice(1),{stdio:'inherit',env:{...process.env,...homes}});
 for(const signal of ['SIGINT','SIGTERM'])process.on(signal,()=>child.kill(signal));
 process.exitCode=await new Promise((resolve,reject)=>{child.once('error',reject);child.once('exit',(code,signal)=>resolve(code??(signal?128:1)));});
}catch(error){console.error(`Sandbox state initialization failed: ${error.message}`);process.exitCode=1;}
