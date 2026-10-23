import test from 'node:test';
import assert from 'node:assert/strict';
import {spawnSync} from 'node:child_process';
import {mkdtempSync,writeFileSync,rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
test('bootstrap provisions native state and restores isolated temporary directory',()=>{
 const root=mkdtempSync(join(tmpdir(),'ww-native-bootstrap-'));
 try {
  const homes=Object.fromEntries(['HOME','TMPDIR','CODEX_HOME','CLAUDE_CONFIG_DIR'].map(name=>[name,join(root,name)]));
  const source=join(root,'native.json');writeFileSync(source,'native-config');
  const state={homes,readRoots:[source],nativeFiles:[{source,home:'CODEX_HOME',path:'config.toml'}]};
  const child=spawnSync(process.execPath,[fileURLToPath(new URL('./initialize-state.mjs',import.meta.url)),JSON.stringify(state),'--',process.execPath,'-e',`const fs=require('fs');if(!fs.existsSync(process.env.CODEX_HOME+'/config.toml'))process.exit(9);process.stdout.write(process.env.TMPDIR);`],{env:{...process.env,...homes,TMPDIR:'/tmp/runtime-overrode-this'},encoding:'utf8'});
  assert.equal(child.status,0,child.stderr); assert.equal(child.stdout,homes.TMPDIR);
 } finally {rmSync(root,{recursive:true,force:true});}
});
