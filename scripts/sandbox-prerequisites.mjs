import { readFile } from 'node:fs/promises';
import { join } from 'node:path';

export async function requireSandbox(root){
 try{
  const installed=JSON.parse(await readFile(join(root,'bridges/sandbox/node_modules/@anthropic-ai/sandbox-runtime/package.json'),'utf8'));
  if(installed.version!=='0.0.81')throw new Error('version mismatch');
 }catch{throw new Error('Install required pinned sandbox runtime: npm ci --prefix bridges/sandbox');}
 if(!['darwin','linux'].includes(process.platform))throw new Error('Sandbox runtime currently supports macOS and Linux only in Whirlwind');
}
