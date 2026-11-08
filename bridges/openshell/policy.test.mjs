import test from 'node:test';
import assert from 'node:assert/strict';
import {buildSandboxPolicy,buildDriverMounts,requireLocalGateway,requirePinnedGateway,workloadEnvironment} from './policy.mjs';

test('OpenShell filesystem policy preserves read-only roles and bounded write grants', () => {
  const profile={cwd:'/work/plan',read_only:true,read_roots:['/git/objects'],write_roots:['/work/plan/.whirlwind/plans'],allowed_paths:[],native_files:[],domains:['old.example']};
  const policy=buildSandboxPolicy(profile,49152,['/**']);
  assert.equal(policy.landlock.compatibility,'hard_requirement');
  assert.deepEqual(policy.filesystem_policy.read_write,['/work/plan/.whirlwind/plans']);
  assert.ok(policy.filesystem_policy.read_only.includes('/work/plan'));
  assert.deepEqual(policy.network_policies.proxy_egress.endpoints,[{host:'host.openshell.internal',port:49152,protocol:'tcp',tls:'skip'}]);
  assert.deepEqual(policy.network_policies.proxy_egress.binaries,[{path:'/**'}]);
  assert.ok(!JSON.stringify(policy).includes('old.example'));
});

test('OpenShell proxy endpoint skips L7 parsing only for its exact TCP host and port', () => {
  const profile={cwd:'/work/plan',read_only:false,read_roots:[],write_roots:[],allowed_paths:['src'],native_files:[]};
  const policy=buildSandboxPolicy(profile,49152,['/usr/bin/node']);
  assert.deepEqual(policy.network_policies.proxy_egress.endpoints,[
    {host:'host.openshell.internal',port:49152,protocol:'tcp',tls:'skip'},
  ]);
  assert.deepEqual(policy.filesystem_policy.read_write,['/work/plan/src']);
});

test('read-only source mounts become writable only to allow Landlock-scoped output paths', () => {
  const profile={cwd:'/work/plan',read_only:true,read_roots:[],write_roots:['/work/plan/.whirlwind/plans'],allowed_paths:[],native_files:[]};
  const mounts=buildDriverMounts(profile);
  assert.deepEqual(mounts,[{type:'bind',source:'/work/plan',target:'/work/plan',read_only:false}]);
  const policy=buildSandboxPolicy(profile,49152,['/**']);
  assert.deepEqual(policy.filesystem_policy.read_write,['/work/plan/.whirlwind/plans']);
  assert.ok(policy.filesystem_policy.read_only.includes('/work/plan'));
});

test('OpenShell host mounts stay worktree scoped and read-only outside approved roots', () => {
  const profile={cwd:'/work/plan',execution_id:'attempt-mount',read_only:false,read_roots:['/opt/acp'],write_roots:[],allowed_paths:['src'],native_files:[{source:'/home/person/.claude/.credentials.json',home:'CLAUDE_CONFIG_DIR',path:'.credentials.json'}]};
  const mounts=buildDriverMounts(profile);
  assert.deepEqual(mounts,[
    {type:'bind',source:'/work/plan',target:'/work/plan',read_only:false},
    {type:'bind',source:'/opt/acp',target:'/opt/acp',read_only:true},
    {type:'bind',source:'/home/person/.claude/.credentials.json',target:'/tmp/ww-native-attempt-mount/CLAUDE_CONFIG_DIR/.credentials.json',read_only:true},
  ]);
  assert.throws(()=>buildDriverMounts({...profile,read_roots:['/home/person']}),/home directory/);
  assert.throws(()=>buildDriverMounts({...profile,native_files:[{source:'/home/person/.ssh/id_ed25519',home:'CODEX_HOME',path:'id_ed25519'}]}),/SSH credential directory/);
});

test('selected credential files mount read-only in the execution-private staging root', () => {
  const profile={cwd:'/work/plan',execution_id:'attempt-123',read_only:true,read_roots:[],write_roots:[],allowed_paths:[],native_files:[{source:'/private/user/.codex/auth.json',home:'CODEX_HOME',path:'auth.json'}]};
  assert.deepEqual(buildDriverMounts(profile),[
    {type:'bind',source:'/work/plan',target:'/work/plan',read_only:true},
    {type:'bind',source:'/private/user/.codex/auth.json',target:'/tmp/ww-native-attempt-123/CODEX_HOME/auth.json',read_only:true},
  ]);
});

test('OpenShell refuses remote or unrecognized active gateway metadata', () => {
  assert.equal(requireLocalGateway([{active:true,is_remote:false,endpoint:'https://127.0.0.1:8080'}]).endpoint,'https://127.0.0.1:8080');
  assert.throws(()=>requireLocalGateway([{active:true,is_remote:true,endpoint:'https://gateway.example'}]),/local gateway/);
  assert.throws(()=>requireLocalGateway([{active:true,endpoint:'https://127.0.0.1:8080'}]),/local gateway/);
});

test('OpenShell lifecycle uses exact pinned gateway even when it is no longer active', () => {
  const gateways=[
    {name:'old-local',endpoint:'https://127.0.0.1:8080',active:false,is_remote:false},
    {name:'new-active',endpoint:'https://127.0.0.1:8081',active:true,is_remote:false},
  ];
  assert.equal(requirePinnedGateway(gateways,'old-local','https://127.0.0.1:8080'),gateways[0]);
  assert.throws(()=>requirePinnedGateway(gateways,'missing','https://127.0.0.1:8090'),/pinned gateway/);
  assert.throws(()=>requirePinnedGateway([{name:'remote',endpoint:'https://example.com',is_remote:false}],'remote','https://example.com'),/loopback/);
});

test('host OpenShell and supervisor bootstrap variables never enter workload env', () => {
  const environment={USER_SECRET:'approved-secret',OPENSHELL_GATEWAY:'host-gateway',OPENSHELL_ADMIN_TOKEN:'admin',WHIRLWIND_OPENSHELL_PROXY_TOKEN:'proxy',WHIRLWIND_CONTROL_TOKEN:'control'};
  assert.deepEqual(workloadEnvironment({environment:['USER_SECRET','OPENSHELL_GATEWAY','OPENSHELL_ADMIN_TOKEN','WHIRLWIND_OPENSHELL_PROXY_TOKEN','WHIRLWIND_CONTROL_TOKEN']},environment),['USER_SECRET']);
});

test('generated guest home, temp, and XDG roots cannot be replaced through profile environment', () => {
  const names=['HOME','CODEX_HOME','CLAUDE_CONFIG_DIR','TMPDIR','TMP','TEMP','XDG_CONFIG_HOME','XDG_STATE_HOME','XDG_CACHE_HOME','USER_SECRET'];
  const environment=Object.fromEntries(names.map(name=>[name,`attacker-${name}`]));
  assert.deepEqual(workloadEnvironment({environment:names},environment),['USER_SECRET']);
});
