import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, writeFile, copyFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawn } from 'node:child_process';

async function request(params) {
  const root = await mkdtemp(join(tmpdir(), 'whirlwind-bridge-test-'));
  const source = (await readFile(new URL('./bridge.mjs', import.meta.url), 'utf8')).replace('@anthropic-ai/claude-agent-sdk', './sdk.mjs');
  await writeFile(join(root, 'bridge.mjs'), source);
  await Promise.all(['bridge-options.mjs', 'bridge-telemetry.mjs'].map(file => copyFile(new URL(file, import.meta.url), join(root, file))));
  await writeFile(join(root, 'sdk.mjs'), `export function query({options}) { return { async *[Symbol.asyncIterator]() {
    if (${params.memoryEnabled === true}) {
      if (options.mcpServers?.['whirlwind-memory']?.command !== '/memory') throw new Error('memory server missing');
      const decision=await options.canUseTool('mcp__whirlwind-memory__search_lessons',{query:'contention'},{signal:new AbortController().signal});
      if(decision.behavior!=='allow') throw new Error('read-only role cannot search memory');
    }
    if (options.model !== 'selected-model') throw new Error('selected model not forwarded');
    if (${params.success === true}) {
      yield {type:'system',subtype:'init',model:'resolved-model',session_id:'s'};
      yield {type:'result',subtype:'success',is_error:false,result:'Hello World',session_id:'s',usage:{input_tokens:10,output_tokens:5},total_cost_usd:0.01};
    } else {yield {type:'result', subtype:'error_during_execution', is_error:true, errors:['rate_limit: usage quota exhausted'], session_id:'s'};}
  }, supportedModels: async () => [{value:'alias', resolvedModel:'selected-model', displayName:'Selected model'}], close() {} }; }`);
  try {
    return await new Promise((resolve, reject) => {
      const child = spawn(process.execPath, [join(root, 'bridge.mjs')]);
      let buffer = '';
      const timer = setTimeout(() => { child.kill(); reject(new Error('bridge did not reply')); }, 4000);
      child.stdout.on('data', data => {
        buffer += data;
        for (const line of buffer.split('\n').slice(0, -1)) {
          const frame = JSON.parse(line);
          if (frame.id === 1) { clearTimeout(timer); child.stdin.end(); resolve(frame); }
        }
      });
      child.on('error', error => { clearTimeout(timer); reject(error); });
      child.on('exit', code => { if (code && !buffer) { clearTimeout(timer); reject(new Error('bridge exited '+code)); } });
      child.stdin.write(JSON.stringify({id:1,method:params.method ?? 'run',params:{cwd:root,prompt:'Hello World',read_only:true,model:'selected-model',...(params.memoryEnabled ? {memory:{command:'/memory',url:'http://127.0.0.1:1',token:'attempt-only'}} : {})}})+'\n');
    });
  } finally { await rm(root, { recursive:true, force:true }); }
}

test('selected Claude model is passed and quota details survive the bridge', async () => {
  const frame = await request({});
  assert.match(frame.error.message, /rate_limit: usage quota exhausted/);
});

test('Claude model catalog is obtained without inference', async () => {
  const frame = await request({method:'models'});
  assert.deepEqual(frame.result.models, [{id:'selected-model',name:'Selected model'}]);
});

test('Claude run records runtime model instead of configured alias',async()=>{
 const frame=await request({success:true});
 assert.equal(frame.result.resolved_model,'resolved-model');
});

test('Claude usage retains input output and measurement scope',async()=>{
 const frame=await request({success:true});
 assert.deepEqual(frame.result.usage,{input_tokens:10,output_tokens:5,cached_tokens:null,total_tokens:15,cost:0.01,scope:'attempt'});
});

test('read-only Claude can query scoped memory without write permission',async()=>{
 const frame=await request({success:true,memoryEnabled:true});
 assert.equal(frame.result?.text,'Hello World');
});
