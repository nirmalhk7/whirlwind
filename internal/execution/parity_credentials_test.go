package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/process"
)

const (
	credentialAllowedPath   = "/allowed"
	credentialFixtureHeader = "X-Fixture-Key"
	credentialExampleOrigin = "https://example.com"
)

func TestParityConfiguredCredentialRoutesReachNativeSandbox(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("native macOS credential acceptance")
	}
	root := t.TempDir()
	cwd := filepath.Join(root, "source")
	if err := os.Mkdir(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(root, "credential")
	if err := os.WriteFile(keyFile, []byte("fixture-private-credential"), 0600); err != nil {
		t.Fatal(err)
	}
	values := map[string]any{"credentials": map[string]any{"fixture": map[string]any{"upstream": "https://credential-test.whirlwind.invalid", "credential_key": "file://" + keyFile, "env_var": "FIXTURE_API_KEY", "inject_header": credentialFixtureHeader, "credential_format": "{}", "endpoint_rules": []map[string]string{{"method": "POST", "path": credentialAllowedPath}}}}}
	raw, _ := json.Marshal(values)
	var policy model.SandboxPolicy
	if err := json.Unmarshal(raw, &policy); err != nil {
		t.Fatal(err)
	}
	profile, err := ForPolicy(cwd, true, "approved-config", &policy)
	if err != nil {
		t.Fatal(err)
	}
	_, source, _, _ := runtime.Caller(0)
	profile.Launcher = filepath.Join(filepath.Dir(source), "..", "..", "bridges", "nono", "launch.mjs")
	t.Setenv("WHIRLWIND_PROCESS_REGISTRY", filepath.Join(root, "processes"))
	code := `const http=require('http');const key=process.env.FIXTURE_API_KEY;if(!key)throw new Error('scoped credential unavailable');const base=process.env.FIXTURE_BASE_URL||new URL(process.env.HTTP_PROXY).origin+'/fixture';const request=http.request(base+'/allowed',{method:'GET',agent:false,headers:{'X-Fixture-Key':key}},response=>{response.resume();response.on('end',()=>{if(response.statusCode!==403)throw new Error('approved endpoint methods lost: '+response.statusCode);console.log('credential-policy-enforced')})});request.on('error',error=>{console.error(error.message);process.exitCode=1});request.end();`
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command, err := Command(ctx, []string{"node", "-e", code}, profile)
	if err != nil {
		t.Fatal(err)
	}
	command.Dir = cwd
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := process.Run(command); err != nil {
		t.Fatalf("configured credential policy lost: %v %s", err, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "credential-policy-enforced" {
		t.Fatalf("native policy not enforced: %s", stdout.String())
	}
}

func TestParityMalformedCredentialPolicyFailsBeforeLaunch(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(map[string]any)
	}{
		{name: "missing endpoint scope", change: func(value map[string]any) { delete(value, "endpoint_rules") }},
		{name: "invalid method", change: func(value map[string]any) {
			value["endpoint_rules"] = []map[string]string{{"method": "invented", "path": credentialAllowedPath}}
		}},
		{name: "bootstrap environment", change: func(value map[string]any) { value["env_var"] = "HOME" }},
		{name: "embedded URL credentials", change: func(value map[string]any) { value["upstream"] = "https://user:secret@example.com" }},
		{name: "remote plaintext upstream", change: func(value map[string]any) { value["upstream"] = "http://example.com" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := map[string]any{"upstream": credentialExampleOrigin, "credential_key": "env://FIXTURE_API_KEY", "env_var": "FIXTURE_API_KEY", "inject_header": credentialFixtureHeader, "credential_format": "{}", "endpoint_rules": []map[string]string{{"method": "POST", "path": credentialAllowedPath}}}
			test.change(value)
			raw, _ := json.Marshal(map[string]any{"credentials": map[string]any{"fixture": value}})
			var policy model.SandboxPolicy
			if err := json.Unmarshal(raw, &policy); err != nil {
				t.Fatal(err)
			}
			if _, err := ForPolicy(t.TempDir(), true, "config", &policy); err == nil {
				t.Fatalf("invalid credential configuration accepted: %s", test.name)
			}
		})
	}
}

func TestParityEnvironmentCredentialSourceStaysOutsideChild(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("native macOS credential acceptance")
	}
	root := t.TempDir()
	t.Setenv("FIXTURE_SOURCE_TOKEN", "fixture-private-environment-credential")
	t.Setenv("WHIRLWIND_PROCESS_REGISTRY", filepath.Join(t.TempDir(), "processes"))
	policy := &model.SandboxPolicy{Credentials: map[string]model.NetworkCredential{"fixture": {Upstream: "https://credential-test.whirlwind.invalid", CredentialKey: "env://FIXTURE_SOURCE_TOKEN", EnvVar: "FIXTURE_API_KEY", InjectHeader: credentialFixtureHeader, CredentialFormat: "{}", EndpointRules: []model.NetworkEndpointRule{{Method: "POST", Path: credentialAllowedPath}}}}}
	profile, err := ForPolicy(root, true, "approved-config", policy)
	if err != nil {
		t.Fatal(err)
	}
	code := `if(process.env.FIXTURE_SOURCE_TOKEN)throw new Error('real credential source reached child');if(!process.env.FIXTURE_API_KEY)throw new Error('phantom credential missing');console.log(JSON.stringify({status:'environment-credential-isolated',phantom:process.env.FIXTURE_API_KEY}))`
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command, err := Command(ctx, []string{"node", "-e", code}, profile)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := process.Run(command); err != nil {
		t.Fatalf("environment credential did not reach only native supervisor: %v %s", err, output.String())
	}
	var result struct {
		Status  string `json:"status"`
		Phantom string `json:"phantom"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "environment-credential-isolated" || result.Phantom == "" || result.Phantom == os.Getenv("FIXTURE_SOURCE_TOKEN") {
		t.Fatal("child did not receive an isolated phantom credential")
	}
}

func TestParityProxyCredentialFileCannotOverlapChildGrants(t *testing.T) {
	for _, scenario := range []string{"worktree", "read root", "runtime", "symlink", "system read root"} {
		t.Run(scenario, func(t *testing.T) {
			assertCredentialFileGrantRejected(t, scenario)
		})
	}
}

func TestParityCredentialRoutesCannotSharePhantomVariable(t *testing.T) {
	first := model.NetworkCredential{Upstream: credentialExampleOrigin, CredentialKey: "env://SOURCE_ONE", EnvVar: "CLIENT_API_KEY", InjectHeader: "Authorization", CredentialFormat: "Bearer {}", EndpointRules: []model.NetworkEndpointRule{{Method: "POST", Path: credentialAllowedPath}}}
	second := first
	second.CredentialKey = "env://SOURCE_TWO"
	policy := &model.SandboxPolicy{Credentials: map[string]model.NetworkCredential{"first": first, "second": second}}
	if _, err := ForPolicy(t.TempDir(), true, "config", policy); err == nil {
		t.Fatal("two services silently shared one phantom variable")
	}
}

func assertCredentialFileGrantRejected(t *testing.T, scenario string) {
	t.Helper()
	root := t.TempDir()
	cwd := filepath.Join(root, "source")
	requireExecutionSuccess(t, os.Mkdir(cwd, 0700))
	keyFile := filepath.Join(root, "credential")
	if scenario == "worktree" || scenario == "symlink" {
		keyFile = filepath.Join(cwd, "credential")
	}
	if scenario == "system read root" {
		keyFile = "/etc/hosts"
	} else if err := os.WriteFile(keyFile, []byte("fixture-private-credential"), 0600); err != nil {
		t.Fatal(err)
	}
	if scenario == "symlink" {
		link := filepath.Join(root, "linked-credential")
		requireExecutionSuccess(t, os.Symlink(keyFile, link))
		keyFile = link
	}
	policy := &model.SandboxPolicy{Credentials: map[string]model.NetworkCredential{"fixture": {Upstream: credentialExampleOrigin, CredentialKey: "file://" + keyFile, EnvVar: "FIXTURE_API_KEY", InjectHeader: credentialFixtureHeader, CredentialFormat: "{}", EndpointRules: []model.NetworkEndpointRule{{Method: "POST", Path: credentialAllowedPath}}}}}
	if scenario == "read root" {
		policy.ReadRoots = []string{keyFile}
	}
	profile, err := ForPolicy(cwd, true, "config", policy)
	if scenario != "runtime" {
		if err == nil {
			t.Fatal("proxy credential file exposed through child filesystem grant")
		}
		return
	}
	requireExecutionSuccess(t, err)
	profile.RuntimeDir = root
	command, err := Command(context.Background(), []string{"/usr/bin/true"}, profile)
	if err == nil {
		_ = process.Forget(command)
		t.Fatal("late runtime grant exposed proxy credential file")
	}
}
