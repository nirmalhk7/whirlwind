import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawn } from 'node:child_process';

async function request(params) {
  const root = await mkdtemp(join(tmpdir(), 'whirlwind-bridge-test-'));
  const source = (await readFile(new URL('./bridge.mjs', import.meta.url), 'utf8')).replace('@anthropic-ai/claude-agent-sdk', './sdk.mjs');
  await writeFile(join(root, 'bridge.mjs'), source);
  const bridgeRun = (await readFile(new URL('./bridge-run.mjs', import.meta.url), 'utf8')).replace('@anthropic-ai/claude-agent-sdk', './sdk.mjs');
  await writeFile(join(root, 'bridge-run.mjs'), bridgeRun);
  await writeFile(join(root, 'native-catalog.mjs'), await readFile(new URL('./native-catalog.mjs', import.meta.url)));
  await writeFile(join(root, 'sdk.mjs'), params.sdkSource ?? `export function query({options}) { return { async *[Symbol.asyncIterator]() {
    if (options.model !== 'selected-model') throw new Error('selected model not forwarded');
    if (${params.checkTool===true}) {
      if(options.mcpServers?.whirlwind_checks?.command!=='node')throw new Error('review check MCP not advertised');
      const allow=await options.canUseTool('mcp__whirlwind_checks__run_check',{check_id:'tests'},{});
      if(allow.behavior!=='allow')throw new Error('registered reviewer check denied');
      const deny=await options.canUseTool('Bash',{command:'bad'},{});
      if(deny.behavior!=='deny')throw new Error('reviewer shell allowed');
    }
    if (${params.success === true}) {
      yield {type:'system',subtype:'init',model:'resolved-model',session_id:'s'};
      yield {type:'result',subtype:'success',is_error:false,result:'Hello World',session_id:'s',usage:${JSON.stringify(params.usage ?? {input_tokens:10,output_tokens:5,cache_read_input_tokens:0,cache_creation_input_tokens:0})},total_cost_usd:0.01};
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
          if (frame.method === 'permission/request' && (params.allowPermissions || params.denyPermissions)) child.stdin.write(JSON.stringify({id:frame.id,result:{allow:params.allowPermissions===true}})+'\n');
          if (frame.id === 1) { clearTimeout(timer); child.stdin.end(); resolve(frame); }
        }
      });
      child.on('error', error => { clearTimeout(timer); reject(error); });
      child.on('exit', code => { if (code && !buffer) { clearTimeout(timer); reject(new Error('bridge exited '+code)); } });
      child.stdin.write(JSON.stringify({id:1,method:params.method ?? 'run',params:{cwd:root,prompt:'Hello World',read_only:true,model:'selected-model',...(params.checkTool?{check_mcp:{command:'node',args:['registered-helper']}}:{}),...params.input}})+'\n');
    });
  } finally { await rm(root, { recursive:true, force:true }); }
}

test('selected Claude model is passed and quota details survive the bridge', async () => {
  const frame = await request({});
  assert.match(frame.error.message, /rate_limit: usage quota exhausted/);
});

test('Claude reviewer exposes only the registered committed-check MCP capability',async()=>{
 const frame=await request({success:true,checkTool:true});
 assert.equal(frame.error,undefined);
 assert.equal(frame.result.text,'Hello World');
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
 assert.equal(frame.result.usage.input_tokens,10); assert.equal(frame.result.usage.total_tokens,15); assert.equal(frame.result.usage.scope,'attempt');
});

// Dropping separate cache buckets would understate the native provider's token consumption.
test('Claude total includes separate cache reads and writes and preserves raw sample',async()=>{
 const usage={input_tokens:10,output_tokens:5,cache_read_input_tokens:20,cache_creation_input_tokens:10};
 const frame=await request({success:true,usage});
 assert.equal(frame.result.usage.total_tokens,45);
 assert.equal(frame.result.usage.cache_write_tokens,10);
 assert.deepEqual(frame.result.usage.raw,usage);
 assert.equal(frame.result.usage.cached_input_semantics,'separate_from_input');
});
test('Claude incomplete native sample does not manufacture total tokens',async()=>{
 const frame=await request({success:true,usage:{input_tokens:10,output_tokens:5}});
 assert.equal(frame.result.usage.total_tokens,null);
 assert.equal(frame.result.usage.completeness,'partial');
});

// SDK subprocess is replaced at its transport boundary; real bridge permission,
// discovery, prompt ordering, and result handling remain under test.
function nativeSDK({catalogOnly = false, statuses, tools = [], effort} = {}) {
  return `
export function query({options,prompt}) {
  if (!options.settings?.permissions?.ask?.includes('mcp__*')) throw new Error('native preapproved tools can bypass host permission policy');
  let discovered = false;
  return {
    async mcpServerStatus() { discovered = true; return ${JSON.stringify(statuses ?? [
      {name:'jenkins',status:'connected',source:'user',config:{type:'http',url:'https://user:secret@internal'},tools:[
        {name:'build',description:'Read build',annotations:{readOnly:true,destructive:false}},
        {name:'delete',annotations:{readOnly:true,destructive:true}},
        {name:'unknown'},
      ]},
      {name:'locked',status:'needs-auth',source:'user',error:'token=secret',tools:[]},
    ])}; },
    async *[Symbol.asyncIterator]() {
      if (${catalogOnly}) throw new Error('catalog discovery invoked inference');
      if (!discovered) throw new Error('prompt started before frozen discovery');
      if (options.effort !== ${JSON.stringify(effort)}) throw new Error('effort not forwarded');
      if (options.settingSources?.length === 0) throw new Error('native sources disabled');
      for await (const input of prompt) { if(input.message.content !== 'Hello World') throw new Error('prompt changed'); break; }
      for (const tool of ${JSON.stringify(tools)}) {
        const decision = await options.canUseTool(tool.name,{},{});
        if(decision.behavior !== tool.expected) throw new Error(tool.name+' '+decision.behavior);
      }
      yield {type:'assistant',message:{role:'assistant',content:[
        {type:'tool_use',id:'read-1',name:'mcp__jenkins__build',input:{}},
        {type:'tool_use',id:'read-2',name:'mcp__jenkins__build',input:{}},
        {type:'tool_use',id:'local-1',name:'Read',input:{}},
      ]}};
      yield {type:'user',message:{role:'user',content:[
        {type:'tool_result',tool_use_id:'read-1',content:'build passed'},
        {type:'tool_result',tool_use_id:'read-2',content:'failure',is_error:true},
        {type:'tool_result',tool_use_id:'absent',content:'invented'},
        {type:'tool_result',tool_use_id:'local-1',content:'local file'},
      ]}};
      yield {type:'result',subtype:'success',result:'Hello World',is_error:false};
    }, close() {}
  };
}`;
}

test('native Claude inventory uses SDK metadata without inference or credential leakage', async () => {
  const frame = await request({sdkSource:nativeSDK({catalogOnly:true}),input:{native_integrations:true,catalog_only:true}});
  assert.equal(frame.error,undefined);
  assert.deepEqual(frame.result.integrations,[
    {provider:'claude',ref:'mcp://jenkins',name:'jenkins',kind:'mcp',enabled:true,callable:true,tools:[
      {name:'mcp__jenkins__build',description:'Read build',read_only:true},
      {name:'mcp__jenkins__delete',read_only:false},
      {name:'mcp__jenkins__unknown',read_only:false},
    ]},
    {provider:'claude',ref:'mcp://locked',name:'locked',kind:'mcp',enabled:true,callable:false,error:'MCP server needs-auth',tools:[]},
  ]);
  assert.equal(JSON.stringify(frame).includes('secret'),false);
});

test('native read-only Claude grants annotated reads and records successful native results',async()=>{
  const frame=await request({sdkSource:nativeSDK({effort:'high',tools:[
    {name:'mcp__jenkins__build',expected:'allow'},
    {name:'mcp__jenkins__delete',expected:'deny'},
    {name:'mcp__jenkins__unknown',expected:'deny'},
    {name:'mcp__missing__read',expected:'deny'},
    {name:'Bash',expected:'deny'},
  ]}),input:{native_integrations:true,effort:'high',required_integrations:[{provider:'claude',ref:'mcp://jenkins'}]}});
  assert.equal(frame.error,undefined);
  assert.deepEqual(frame.result.tool_calls,[{ref:'mcp://jenkins',tool:'mcp__jenkins__build',call_id:'read-1',success:true},{ref:'mcp://jenkins',tool:'mcp__jenkins__build',call_id:'read-2',success:false}]);
});

test('native Claude rejects disconnected or mismatched required integrations before inference',async()=>{
  for(const required of [{provider:'claude',ref:'mcp://locked'},{provider:'codex',ref:'mcp://jenkins'},{provider:'claude',ref:'mcp://absent'}]){
    const frame=await request({sdkSource:nativeSDK({catalogOnly:true}),input:{native_integrations:true,required_integrations:[required]}});
    assert.match(frame.error?.message??'',/Required integration/);
  }
});

test('native write-capable Claude routes external tools through permission inbox',async()=>{
  const frame=await request({sdkSource:nativeSDK({tools:[{name:'mcp__jenkins__delete',expected:'allow'}]}),allowPermissions:true,input:{native_integrations:true,read_only:false}});
  assert.equal(frame.error,undefined);
});

test('native check tool trusts SDK provenance rather than collision with server name',async()=>{
  for(const source of ['user','sdk']){
    const frame=await request({checkTool:true,sdkSource:nativeSDK({statuses:[{name:'whirlwind_checks',source,status:'connected',tools:[{name:'run_check'}]}],tools:[{name:'mcp__whirlwind_checks__run_check',expected:source==='sdk'?'allow':'deny'}]}),input:{native_integrations:true}});
    assert.equal(frame.error,undefined);
  }
});

test('native Claude resolves normalized MCP names and denies colliding aliases',async()=>{
  const frame=await request({sdkSource:nativeSDK({statuses:[
    {name:'build.server',status:'connected',tools:[{name:'read',annotations:{readOnly:true}}]},
    {name:'a.b',status:'connected',tools:[{name:'read',annotations:{readOnly:true}}]},
    {name:'a_b',status:'connected',tools:[{name:'read',annotations:{readOnly:true}}]},
  ],tools:[{name:'mcp__build_server__read',expected:'allow'},{name:'mcp__a_b__read',expected:'deny'}]}),input:{native_integrations:true}});
  assert.equal(frame.error,undefined);
  assert.equal(frame.result.integrations[0].tools[0].name,'mcp__build_server__read');
});

test('native Claude preserves configured provider identity in inventory and requirements',async()=>{
  const frame=await request({sdkSource:nativeSDK(),input:{native_integrations:true,provider_id:'work-claude',required_integrations:[{provider:'work-claude',ref:'mcp://jenkins'}]}});
  assert.equal(frame.error,undefined);
  assert.equal(frame.result.integrations[0].provider,'work-claude');
});

test('coding Claude advertises and permits registered SDK host checks without inbox permission',async()=>{
  const sdkSource=nativeSDK({statuses:[{name:'whirlwind_checks',source:'sdk',status:'connected',tools:[{name:'run_check'}]}],tools:[{name:'mcp__whirlwind_checks__run_check',expected:'allow'}]}).replace('let discovered = false;',"if(options.mcpServers?.whirlwind_checks?.command !== 'node') throw new Error('coding check MCP not advertised'); let discovered = false;");
  const frame=await request({sdkSource,checkTool:true,denyPermissions:true,input:{native_integrations:true,read_only:false}});
  assert.equal(frame.error,undefined);
});
