// Package openshell provides an explicit test-only OpenShell CLI fixture.
// It validates the command surface and schema; it does not enforce OS isolation.
package openshell

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type Fixture struct {
	Directory string
	Binary    string
	CallsPath string
	EnvPath   string
	statePath string
	guestPath string
}

type EnvironmentCall struct {
	Operation       string `json:"operation"`
	XDGConfigHome   string `json:"xdg_config_home,omitempty"`
	XDGStateHome    string `json:"xdg_state_home,omitempty"`
	XDGPresent      bool   `json:"xdg_present"`
	XDGStatePresent bool   `json:"xdg_state_present"`
}

// Install creates a test-scoped CLI with pinned v0.1.7-shaped gateway and sandbox responses.
// The fake never executes an ACP command unless EnableCommandExecution is called.
func Install(t testing.TB) *Fixture {
	t.Helper()
	_, source, _, _ := runtime.Caller(0)
	t.Setenv("WHIRLWIND_EXECUTION_LAUNCHER", filepath.Join(filepath.Dir(source), "..", "..", "..", "bridges", "openshell", "launch.mjs"))
	directory := t.TempDir()
	fixture := &Fixture{
		Directory: directory,
		Binary:    filepath.Join(directory, "openshell"),
		CallsPath: filepath.Join(directory, "calls.jsonl"),
		EnvPath:   filepath.Join(directory, "environment.jsonl"),
		statePath: filepath.Join(directory, "state.json"),
		guestPath: filepath.Join(directory, "guest"),
	}
	initialState := `{"sandboxes":{},"inert_init":false,"runtime_home_scoped":false,"fail_create":false,"fail_list":false,"gateway_status":"connected","authentication_status":"not_required","info_status":"healthy","synthetic_native_root":"","native_mounts":[],"guest_root":__GUEST_ROOT__}`
	initialState = strings.ReplaceAll(initialState, "__GUEST_ROOT__", quote(filepath.ToSlash(fixture.guestPath)))
	if err := os.WriteFile(fixture.statePath, []byte(initialState), 0600); err != nil {
		t.Fatal(err)
	}
	script := strings.ReplaceAll(fakeCLI, "__STATE_PATH__", quote(filepath.ToSlash(fixture.statePath)))
	script = strings.ReplaceAll(script, "__CALLS_PATH__", quote(filepath.ToSlash(fixture.CallsPath)))
	script = strings.ReplaceAll(script, "__ENV_PATH__", quote(filepath.ToSlash(fixture.EnvPath)))
	if err := os.WriteFile(fixture.Binary, []byte("#!/usr/bin/env node\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(fixture.Binary, 0700); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", directory+string(os.PathListSeparator)+oldPath)
	return fixture
}

// ProjectSyntheticNativeFiles permits only selected provider auth files from
// a synthetic directory inside this fixture's private temporary root.
func (f *Fixture) ProjectSyntheticNativeFiles(t testing.TB, root string) {
	t.Helper()
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	fixtureRoot, err := filepath.EvalSymlinks(f.Directory)
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(fixtureRoot, resolvedRoot)
	if err != nil {
		t.Fatal(err)
	}
	if relative == "." || relative == ".." {
		t.Fatal("synthetic native auth root must be a child of the fake OpenShell fixture")
	}
	if strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		t.Fatal("synthetic native auth root must be a child of the fake OpenShell fixture")
	}
	info, err := os.Stat(resolvedRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatal("synthetic native auth root must be a directory")
	}
	f.updateState(t, map[string]any{"synthetic_native_root": resolvedRoot})
}

// EnableCommandExecution allows this fake to run the command following `--` in the host test process.
// Use only for protocol fixtures with disposable commands; it is not a sandbox or isolation test.
func (f *Fixture) EnableCommandExecution() {
	contents, err := os.ReadFile(f.Binary)
	if err != nil {
		panic(err)
	}
	script := strings.ReplaceAll(string(contents), "const executePayload = false;", "const executePayload = true;")
	if err := os.WriteFile(f.Binary, []byte(script), 0700); err != nil {
		panic(err)
	}
}

// EnableGoTestPayloadExecution enables a controlled Go test payload with only
// test-owned HOME and GOCACHE paths added to its otherwise minimal environment.
func (f *Fixture) EnableGoTestPayloadExecution(home, cache string) {
	if !filepath.IsAbs(home) || !filepath.IsAbs(cache) {
		panic("Go test payload HOME and GOCACHE must be absolute fixture paths")
	}
	contents, err := os.ReadFile(f.Binary)
	if err != nil {
		panic(err)
	}
	const environment = "const payloadEnvironment = {};"
	if !strings.Contains(string(contents), environment) {
		panic("fake OpenShell payload environment marker is missing")
	}
	envJSON, err := json.Marshal(map[string]string{"HOME": home, "GOCACHE": cache})
	if err != nil {
		panic(err)
	}
	script := strings.ReplaceAll(string(contents), "const executePayload = false;", "const executePayload = true;")
	script = strings.ReplaceAll(script, environment, "const payloadEnvironment = "+string(envJSON)+";")
	if err := os.WriteFile(f.Binary, []byte(script), 0700); err != nil {
		panic(err)
	}
}

// Calls returns the recorded CLI argument vectors. Proxy or model credentials are never recorded.
func (f *Fixture) Calls(t testing.TB) [][]string {
	t.Helper()
	file, err := os.Open(f.CallsPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	var calls [][]string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var call []string
		if err := json.Unmarshal(scanner.Bytes(), &call); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, call)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return calls
}

// EnvironmentCalls reports only the requested XDG path metadata, never other environment values.
func (f *Fixture) EnvironmentCalls(t testing.TB) []EnvironmentCall {
	t.Helper()
	file, err := os.Open(f.EnvPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	var calls []EnvironmentCall
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var call EnvironmentCall
		if err := json.Unmarshal(scanner.Bytes(), &call); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, call)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return calls
}

// EnvironmentSeedKeys returns names extracted from sandbox env bootstrap input; values are never stored.
func (f *Fixture) EnvironmentSeedKeys(t testing.TB) []string {
	t.Helper()
	data, err := os.ReadFile(f.statePath)
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Keys []string `json:"environment_seed_keys"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	return state.Keys
}

// RuntimeHomeScoped reports whether bootstrap supplied the execution-private guest home layout.
func (f *Fixture) RuntimeHomeScoped(t testing.TB) bool {
	t.Helper()
	data, err := os.ReadFile(f.statePath)
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Scoped bool `json:"runtime_home_scoped"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	return state.Scoped
}

func (f *Fixture) ResetCalls(t testing.TB) {
	t.Helper()
	if err := os.WriteFile(f.CallsPath, nil, 0600); err != nil {
		t.Fatal(err)
	}
}

// FailCreate makes the next and later sandbox create requests fail without logging payloads.
func (f *Fixture) FailCreate(t testing.TB) {
	t.Helper()
	f.updateFailure(t, "fail_create")
}

// FailList makes sandbox reconciliation fail, allowing cleanup-error handling tests.
func (f *Fixture) FailList(t testing.TB) {
	t.Helper()
	f.updateFailure(t, "fail_list")
}

// SetGatewayHealth controls pinned CLI status fields for fail-closed launcher tests.
func (f *Fixture) SetGatewayHealth(t testing.TB, status, authentication, info string) {
	t.Helper()
	f.updateState(t, map[string]any{"gateway_status": status, "authentication_status": authentication, "info_status": info})
}

func (f *Fixture) updateFailure(t testing.TB, field string) {
	t.Helper()
	f.updateState(t, map[string]any{field: true})
}

func (f *Fixture) updateState(t testing.TB, updates map[string]any) {
	t.Helper()
	data, err := os.ReadFile(f.statePath)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	for field, value := range updates {
		state[field] = value
	}
	data, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.statePath, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func (f *Fixture) HasInertInit(t testing.TB) bool {
	t.Helper()
	state, err := os.ReadFile(f.statePath)
	if err != nil {
		t.Fatal(err)
	}
	var value struct {
		InertInit bool `json:"inert_init"`
	}
	if err := json.Unmarshal(state, &value); err != nil {
		t.Fatal(err)
	}
	return value.InertInit
}

func quote(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

const fakeCLI = `
const fs = require('node:fs');
const cp = require('node:child_process');
const path = require('node:path');
const statePath = __STATE_PATH__;
const callsPath = __CALLS_PATH__;
const envPath = __ENV_PATH__;
const executePayload = false;
const payloadEnvironment = {};
const args = process.argv.slice(2);
const safeArgs = () => {
  const separator = args.indexOf('--');
  const visible = separator < 0 ? args : [...args.slice(0, separator), '--', '[test payload omitted]'];
  return visible.map((value,index) => /token|secret|password|authorization|credential/i.test(value) ? '[redacted]' : value);
};
fs.appendFileSync(callsPath, JSON.stringify(safeArgs()) + '\n', {mode:0o600});
fs.appendFileSync(envPath, JSON.stringify({operation:args[0] === 'sandbox' ? args[1] : args[0],xdg_present:Object.hasOwn(process.env,'XDG_CONFIG_HOME'),...(process.env.XDG_CONFIG_HOME ? {xdg_config_home:process.env.XDG_CONFIG_HOME} : {}),xdg_state_present:Object.hasOwn(process.env,'XDG_STATE_HOME'),...(process.env.XDG_STATE_HOME ? {xdg_state_home:process.env.XDG_STATE_HOME} : {})}) + '\n', {mode:0o600});
const readState = () => JSON.parse(fs.readFileSync(statePath, 'utf8'));
const writeState = value => fs.writeFileSync(statePath, JSON.stringify(value), {mode:0o600});
const output = value => process.stdout.write(JSON.stringify(value) + '\n');
const valueAfter = name => { const index = args.indexOf(name); return index < 0 ? '' : args[index + 1]; };
const selectedAuth = new Map([
  ['.codex/auth.json', {home:'CODEX_HOME',path:'auth.json'}],
  ['.claude/.credentials.json', {home:'CLAUDE_CONFIG_DIR',path:'.credentials.json'}],
  ['.cursor/cli-config.json', {home:'HOME',path:'.cursor/cli-config.json'}],
]);
const fakeGuestPath = (state, guestPath) => {
  if (typeof guestPath !== 'string' || !path.posix.isAbsolute(guestPath)) throw new Error('fake guest path must be absolute');
  const root = path.resolve(state.guest_root);
  const mapped = path.resolve(root, '.' + guestPath);
  if (mapped === root || !mapped.startsWith(root + path.sep)) throw new Error('fake guest path escaped its private root');
  return mapped;
};
const configuredSyntheticMounts = (state, config) => {
  if (!state.synthetic_native_root) return [];
  const mounts = Object.values(config).flatMap(driver => driver?.mounts ?? []);
  const root = path.resolve(state.synthetic_native_root);
  return mounts.flatMap(mount => {
    if (mount?.type !== 'bind' || mount.read_only !== true || typeof mount.source !== 'string' || typeof mount.target !== 'string') return [];
    const source = path.resolve(mount.source);
    const relative = path.relative(root, source).split(path.sep).join('/');
    const expected = selectedAuth.get(relative);
    const target = /^\/tmp\/ww-native-[A-Za-z0-9_-]+\/(CODEX_HOME|CLAUDE_CONFIG_DIR|HOME)\/(.+)$/.exec(mount.target);
    if (!expected || !target || target[1] !== expected.home || target[2] !== expected.path) return [];
    return [{source, target:mount.target, home:expected.home, file_path:expected.path}];
  });
};
const stageSyntheticMounts = state => {
  for (const mount of state.native_mounts ?? []) {
    const staged = fakeGuestPath(state, mount.target);
    fs.mkdirSync(path.dirname(staged), {recursive:true,mode:0o700});
    fs.copyFileSync(mount.source, staged);
    fs.chmodSync(staged, 0o600);
  }
};
const guestRuntimeEnvironment = state => {
  const target = state.native_mounts?.[0]?.target ?? '';
  const match = /^\/tmp\/ww-native-([A-Za-z0-9_-]+)\//.exec(target);
  const runtime = '/tmp/ww-' + (match?.[1] ?? 'fake-runtime');
  return {HOME:runtime+'/home',CODEX_HOME:runtime+'/home/codex',CLAUDE_CONFIG_DIR:runtime+'/home/claude',TMPDIR:runtime+'/tmp'};
};
const payloadEnvironmentFromState = state => {
  const guestEnvironment = guestRuntimeEnvironment(state);
  const environment = Object.fromEntries(Object.entries(guestEnvironment).map(([name,value]) => [name, fakeGuestPath(state, value)]));
  for (const name of ['HOME','CODEX_HOME','CLAUDE_CONFIG_DIR','TMPDIR']) fs.mkdirSync(environment[name], {recursive:true,mode:0o700});
  return environment;
};
const projectSyntheticHomes = (state, guestEnvironment) => {
  for (const mount of state.native_mounts ?? []) {
    const staged = fakeGuestPath(state, mount.target);
    if (!fs.existsSync(staged)) throw new Error('selected synthetic auth mount was not staged');
    const guestHome = guestEnvironment[mount.home];
    if (!guestHome) throw new Error('selected synthetic auth destination home was not bootstrapped');
    const destination = fakeGuestPath(state, path.posix.join(guestHome, mount.file_path));
    fs.mkdirSync(path.dirname(destination), {recursive:true,mode:0o700});
    fs.copyFileSync(staged, destination);
    fs.chmodSync(destination, 0o600);
  }
};
const isSandboxCommand = args[0] === 'sandbox';
if (isSandboxCommand && (!valueAfter('--gateway') || !valueAfter('--gateway-endpoint') || !valueAfter('--workspace'))) {
  process.stderr.write('sandbox commands require pinned gateway, endpoint, and workspace\\n');
  process.exit(64);
}
const gateway = {name:'whirlwind-test-local',endpoint:'http://127.0.0.1:8080',type:'docker',source:'local',auth:'none',active:true,is_remote:false,remote_host:null,resolved_host:'127.0.0.1'};
const sandboxValue = (name, phase='Ready') => {
  const label = valueAfter('--label');
  const [labelKey,labelValue] = label.split('=',2);
  return {id:'test-sandbox-' + name,name,workspace:valueAfter('--workspace'),host_key_fingerprint:'',labels:labelKey ? {[labelKey]:labelValue} : {},annotations:{},resource_version:1,created_at:'2026-01-01T00:00:00Z',phase,current_policy_version:1,exit_code:null,conditions:[],endpoint_statuses:[],last_token_grant_failure:null,configuration_admission:null,provisioning:null,created_from_workload_template:null};
};
if (args[0] === 'gateway' && args[1] === 'list') output([gateway]);
else if (args[0] === 'status') { const state=readState(); output({gateway:gateway.name,server:gateway.endpoint,status:state.gateway_status,authentication:{status:state.authentication_status},version:'v0.1.7'}); }
else if (args[0] === 'gateway' && args[1] === 'info') { const state=readState(); output({gateway:gateway.name,server:gateway.endpoint,status:state.info_status,version:'v0.1.7',compute_drivers:[{name:'docker',capabilities:{driver_name:'docker',driver_version:'test'}}],extensions:[]}); }
else if (args[0] === 'sandbox' && args[1] === 'create') {
  const name = valueAfter('--name');
  if (!name || !valueAfter('--from') || !valueAfter('--policy') || !valueAfter('--driver-config-json')) process.exit(64);
  if (name.length>19) {
    process.stderr.write('sandbox name exceeds the v0.1.7 maximum length of 19 characters\n');
    process.exit(64);
  }
  if (!args.includes('--no-auto-providers')) {
    process.stderr.write('managed sandbox create requires --no-auto-providers\n');
    process.exit(64);
  }
  if (args.includes('--no-keep') && (args.includes('--output') || args.includes('-o'))) {
    process.stderr.write('sandbox create does not support --no-keep with structured output\n');
    process.exit(64);
  }
  const state = readState();
  if (state.fail_create) { process.stderr.write('controlled create failure\n'); process.exit(73); }
  let driverConfig;
  try { driverConfig = JSON.parse(valueAfter('--driver-config-json')); } catch { process.stderr.write('invalid driver mount configuration\n'); process.exit(64); }
  state.native_mounts = configuredSyntheticMounts(state, driverConfig);
  if (executePayload) stageSyntheticMounts(state);
  const separator = args.indexOf('--');
  const command = separator < 0 ? [] : args.slice(separator + 1);
  if (args.includes('--detach')) {
    if (JSON.stringify(command) !== JSON.stringify(['/bin/sh','-c','exec sleep infinity'])) {
      process.stderr.write('detached sandbox init must be an inert sleep command\\n');
      process.exit(64);
    }
    state.inert_init = true;
  }
  state.sandboxes[name] = sandboxValue(name);
  writeState(state);
  if (separator >= 0 && !args.includes('--detach') && executePayload) {
    const command = args.slice(separator + 1);
    const result = cp.spawnSync(command[0], command.slice(1), {stdio:'inherit',env:process.env});
    if (result.error) { process.stderr.write(result.error.message + '\n'); process.exit(127); }
    process.exit(result.status ?? 1);
  }
  output(state.sandboxes[name]);
}
else if (args[0] === 'sandbox' && args[1] === 'get') {
  const state = readState();
  const sandbox = state.sandboxes[args[2]];
  if (!sandbox) process.exit(1);
  output(sandbox);
}
else if (args[0] === 'sandbox' && args[1] === 'list') {
  if(args.includes('--label-selector')) {
    process.stderr.write('sandbox list uses --selector in OpenShell v0.1.7\n');
    process.exit(64);
  }
  if(args.includes('--selector')&&!valueAfter('--selector')) {
    process.stderr.write('sandbox list selector value is required\n');
    process.exit(64);
  }
  const state=readState();
  if(state.fail_list) { process.stderr.write('controlled list failure\n'); process.exit(74); }
  output({sandboxes:Object.values(state.sandboxes),next_page_token:''});
}
else if (args[0] === 'sandbox' && args[1] === 'exec') {
  const separator = args.indexOf('--');
  if (separator < 0) process.exit(64);
  const command = args.slice(separator + 1);
  if (command[0] === '/bin/sh' && command[2]?.includes('cat >') && (command.some(value=>value.startsWith('/tmp/ww-')) || /cat > \/tmp\/ww-/.test(command[2]))) {
    const contents=fs.readFileSync(0,'utf8');
    const keys=[...contents.matchAll(/^export ([A-Za-z_][A-Za-z0-9_]*)=/gm)].map(match=>match[1]).sort();
    const values=Object.fromEntries([...contents.matchAll(/^export ([A-Za-z_][A-Za-z0-9_]*)='([^']*)'$/gm)].map(match=>[match[1],match[2]]));
    const match=/^(\/tmp\/ww-[A-Za-z0-9_-]+)\/home$/.exec(values.HOME??'');
    const scoped=!!match&&values.CODEX_HOME===match[1]+'/home/codex'&&values.CLAUDE_CONFIG_DIR===match[1]+'/home/claude'&&values.TMPDIR===match[1]+'/tmp';
    const state=readState();
    state.environment_seed_keys=keys;
    state.runtime_home_scoped=scoped;
    writeState(state);
    process.exit(0);
  }
  if (command[0] === '/bin/sh' && command[2]?.startsWith('set -a; . "$1" || exit;') && command[4]?.startsWith('/tmp/ww-')) {
    const state=readState();
    if (!Array.isArray(state.environment_seed_keys) || !state.environment_seed_keys.includes('HTTP_PROXY')) {
      process.stderr.write('environment bootstrap was not staged before agent exec\n');
      process.exit(64);
    }
    if (!executePayload) { process.stderr.write('test fixture payload execution is disabled\n'); process.exit(125); }
    const payload=command.slice(5);
    if (!payload.length) process.exit(64);
    const guestEnvironment=guestRuntimeEnvironment(state);
    try { projectSyntheticHomes(state,guestEnvironment); } catch (error) { process.stderr.write(error.message+'\n'); process.exit(65); }
    const result=cp.spawnSync(payload[0],payload.slice(1),{stdio:'inherit',cwd:valueAfter('--workdir')||undefined,env:{PATH:process.env.PATH??'',LANG:process.env.LANG??'C',LC_ALL:process.env.LC_ALL??'C',...payloadEnvironmentFromState(state),...payloadEnvironment}});
    if (result.error) { process.stderr.write(result.error.message + '\n'); process.exit(127); }
    process.exit(result.status ?? 1);
  }
  if (!executePayload) { process.stderr.write('test fixture payload execution is disabled\n'); process.exit(125); }
  const state = readState();
  const guestEnvironment=guestRuntimeEnvironment(state);
  try { projectSyntheticHomes(state,guestEnvironment); } catch (error) { process.stderr.write(error.message+'\n'); process.exit(65); }
  const result = cp.spawnSync(command[0], command.slice(1), {stdio:'inherit',cwd:valueAfter('--workdir')||undefined,env:{PATH:process.env.PATH??'',LANG:process.env.LANG??'C',LC_ALL:process.env.LC_ALL??'C',...payloadEnvironmentFromState(state),...payloadEnvironment}});
  if (result.error) { process.stderr.write(result.error.message + '\n'); process.exit(127); }
  process.exit(result.status ?? 1);
}
else if (args[0] === 'sandbox' && args[1] === 'stop') {
  if (args.includes('--output') || args.includes('-o')) {
    process.stderr.write('sandbox stop does not support structured output\n');
    process.exit(64);
  }
  const state = readState();
  const name = args[2];
  if (!state.sandboxes[name]) process.exit(1);
  state.sandboxes[name].phase = 'Stopped';
  writeState(state);
  output(state.sandboxes[name]);
}
else if (args[0] === 'sandbox' && args[1] === 'delete') {
  if (args.includes('--output') || args.includes('-o')) {
    process.stderr.write('sandbox delete does not support structured output\n');
    process.exit(64);
  }
  const state = readState();
  delete state.sandboxes[args[2]];
  writeState(state);
  output({deleted:true});
}
else { process.stderr.write('unsupported fake OpenShell command\n'); process.exit(64); }
`

// RunCLI is available to fixture tests that need to validate the source-backed JSON schema.
func (f *Fixture) RunCLI(t testing.TB, args ...string) ([]byte, error) {
	t.Helper()
	command := exec.Command(f.Binary, args...)
	output, err := command.Output()
	if err != nil {
		return output, err
	}
	return output, nil
}

// RunCLIWithInput sends a test payload through stdin without placing it in args or fixture logs.
func (f *Fixture) RunCLIWithInput(t testing.TB, input io.Reader, args ...string) ([]byte, error) {
	t.Helper()
	command := exec.Command(f.Binary, args...)
	command.Stdin = input
	output, err := command.Output()
	return output, err
}
