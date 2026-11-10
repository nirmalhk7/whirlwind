import {join,isAbsolute} from 'node:path';

const escapeRegex=value=>value.replace(/[.+^${}()|[\]\\]/g,'\\$&');

// Seatbelt evaluates canonical paths, so a link cannot redirect an allowed
// lexical name into a different writable subtree.
export function codingRules(profile,cwd){
 if(profile.read_only)return [];
 return (profile.allowed_paths??[]).map(pattern=>{
  if(isAbsolute(pattern)||pattern.split(/[\\/]/).includes('..')||/[\[\]{}\x00\n]/.test(pattern))throw new Error('Unsupported or unbounded coding scope');
  const path=join(cwd,pattern);
  if(pattern==='**')return `(allow file-write* (subpath ${JSON.stringify(cwd)}))`;
  if(!/[*?]/.test(pattern))return `(allow file-write* (literal ${JSON.stringify(path)}))`;
  const recursive=pattern.endsWith('/**');
  const base=recursive?path.slice(0,-3):path;
  const regex=escapeRegex(base).replace(/\*\*/g,'\u0001').replace(/\*/g,'[^/]*').replace(/\?/g,'[^/]').replaceAll('\u0001','.*');
  return `(allow file-write* (regex ${JSON.stringify(`^${regex}${recursive?'(/.*)?':''}$`)}))`;
 });
}
