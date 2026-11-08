package provider

import (
	"context"
	"path/filepath"
	"testing"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/testutil/openshell"
)

func TestCheckConnectionUsesPinnedStandaloneRuntime(t *testing.T) {
	fixture := openshell.Install(t)
	fixture.EnableCommandExecution()
	t.Setenv("WHIRLWIND_OPENSHELL_CLI", fixture.Binary)
	t.Setenv("WHIRLWIND_OPENSHELL_IMAGE", "provider-status-test-image")
	peer := `const readline=require('node:readline');readline.createInterface({input:process.stdin}).on('line',line=>{const request=JSON.parse(line);if(request.id===undefined)return;const result=request.method==='initialize'?{protocolVersion:1,agentCapabilities:{},authMethods:[]}:request.method==='session/new'?{sessionId:'metadata-session'}:undefined;process.stdout.write(JSON.stringify(result===undefined?{jsonrpc:'2.0',id:request.id,error:{code:-32601,message:'metadata only'}}:{jsonrpc:'2.0',id:request.id,result})+'\n')});`
	command := []string{"node", "-e", peer}
	supervisorDir := filepath.Join(t.TempDir(), "supervisor", "standalone")
	got, err := CheckConnectionWithSupervisorDir(context.Background(), model.Provider{ID: "probe", Kind: "acp", Command: command}, t.TempDir(), supervisorDir)
	if err != nil {
		t.Fatalf("protocol check failed: %v; OpenShell calls: %#v", err, fixture.Calls(t))
	}
	if got != "Protocol session responding; inference not tested." {
		t.Fatalf("unexpected protocol result %q", got)
	}
	if calls := fixture.Calls(t); len(calls) == 0 {
		t.Fatal("protocol check did not invoke OpenShell")
	}
	entries, err := execution.ReadStandaloneRuntimes(supervisorDir)
	if err != nil {
		t.Fatalf("read standalone runtime registry: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("completed metadata check left standalone runtime entries: %+v", entries)
	}
}
