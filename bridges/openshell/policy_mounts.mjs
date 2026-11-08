import {bounded,nativeCredentialTarget,pathWithin,sensitiveMount,writablePaths} from './policy_filesystem.mjs';

function nativeSources(profile) {
  return new Set((profile.native_files??[]).map(file=>file.source));
}

function addReadMount(mounts,root,cwd) {
  if(sensitiveMount(root)) throw new Error('OpenShell cannot mount an entire home directory or SSH credential directory');
  if(pathWithin(cwd,root)) return;
  if(mounts.some(mount=>mount.source===root)) return;
  mounts.push({type:'bind',source:root,target:root,read_only:true});
}

function addNativeMount(mounts,profile,file) {
  const source=bounded(file.source,'credential source');
  if(sensitiveMount(source)) throw new Error('OpenShell cannot mount an SSH credential directory');
  mounts.push({type:'bind',source,target:nativeCredentialTarget(profile,file),read_only:true});
}

function addWriteMounts(mounts,paths,cwd) {
  for(const path of paths) {
    const root=bounded(path,'write grant');
    if(pathWithin(cwd,root)||mounts.some(mount=>mount.source===root)) continue;
    mounts.push({type:'bind',source:root,target:root,read_only:false});
  }
}

export function buildDriverMounts(profile) {
  const cwd=bounded(profile.cwd,'worktree');
  const roleWrites=writablePaths(profile).map(path=>bounded(path,'write grant'));
  const worktreeNeedsWrite=roleWrites.some(path=>pathWithin(cwd,path));
  const mounts=[{type:'bind',source:cwd,target:cwd,read_only:!!profile.read_only&&!worktreeNeedsWrite}];
  const credentialSources=nativeSources(profile);
  const readRoots=(profile.read_roots??[]).filter(path=>!credentialSources.has(path)).map(path=>bounded(path,'read grant'));
  for(const root of readRoots) addReadMount(mounts,root,cwd);
  for(const file of profile.native_files??[]) addNativeMount(mounts,profile,file);
  addWriteMounts(mounts,roleWrites,cwd);
  return mounts;
}
