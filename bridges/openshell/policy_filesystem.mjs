import {homedir} from 'node:os';
import {isAbsolute,join,normalize,sep} from 'node:path';

export const runtimePaths=['/bin','/etc','/lib','/lib64','/usr'];
const credentialHomes=['HOME','CODEX_HOME','CLAUDE_CONFIG_DIR'];

function invalidBoundedPath(path) {
  const invalidType=typeof path!=='string'||!isAbsolute(path);
  const invalidTraversal=typeof path==='string'&&(path.includes('\0')||path.split(/[\\/]/).includes('..'));
  const invalidNormalization=typeof path==='string'&&(normalize(path)!==path||path===sep);
  return invalidType||invalidTraversal||invalidNormalization;
}

export function bounded(path,label) {
  if(invalidBoundedPath(path)) throw new Error(`${label} must be an absolute bounded path`);
  return path;
}

export function pathWithin(root,path) {
  return path===root||path.startsWith(`${root}${sep}`);
}

export function sensitiveMount(path) {
  const clean=normalize(path);
  const home=normalize(homedir());
  const protectedRoot=clean===home||clean===normalize('/home')||clean===normalize('/Users');
  const parts=clean.split(sep).filter(Boolean);
  const namedUserHome=(parts[0]==='home'||parts[0]==='Users')&&parts.length===2;
  const sshDirectory=parts.includes('.ssh');
  return protectedRoot||namedUserHome||sshDirectory;
}

export function writablePaths(profile) {
  if(profile.read_only) return [...(profile.write_roots??[])];
  const values=[];
  for(const path of profile.allowed_paths??[]) {
    if(path==='**') {
      values.push(profile.cwd);
      continue;
    }
    const invalid=typeof path!=='string'||!path||isAbsolute(path)||path.split(/[\\/]/).includes('..');
    if(invalid) throw new Error('Invalid role write grant');
    values.push(join(profile.cwd,path));
  }
  values.push(...(profile.write_roots??[]));
  return values;
}

export function nativeCredentialTarget(profile,file) {
  const invalidHome=!credentialHomes.includes(file.home);
  const invalidPath=typeof file.path!=='string'||!file.path||isAbsolute(file.path)||file.path.split(/[\\/]/).includes('..');
  if(invalidHome||invalidPath) throw new Error('Invalid scoped native credential mount');
  if(typeof profile.execution_id!=='string'||!/^[A-Za-z0-9_-]+$/.test(profile.execution_id))
    throw new Error('OpenShell native credentials require a valid execution identity');
  return join('/tmp',`ww-native-${profile.execution_id}`,file.home,file.path);
}

export function buildFilesystemPolicy(profile) {
  const cwd=bounded(profile.cwd,'worktree');
  const writable=[...new Set(writablePaths(profile).map(path=>bounded(path,'write grant')))];
  const nativeFiles=profile.native_files??[];
  const nativeSources=new Set(nativeFiles.map(file=>file.source));
  const credentialDestinations=nativeFiles.map(file=>nativeCredentialTarget(profile,file));
  const readRoots=(profile.read_roots??[]).filter(path=>!nativeSources.has(path)).map(path=>bounded(path,'read grant'));
  const readOnly=[...new Set([...runtimePaths,cwd,...readRoots,...credentialDestinations])].filter(path=>!writable.includes(path));
  return {read_only:readOnly,read_write:writable};
}
