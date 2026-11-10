package provider

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"whirlwind/internal/execution"
)

// Leaving Codex's nested Seatbelt enabled makes real tool reads fail under nono.
func TestNonoCodexUsesEnforcedOuterSandbox(t *testing.T) {
	ack := filepath.Join(t.TempDir(), "mode")
	script := `const fs=require('fs'),rl=require('readline').createInterface({input:process.stdin});rl.on('line',line=>{const m=JSON.parse(line);if(m.method==='session/set_mode'&&m.params.sessionId==='native-session'&&m.params.modeId==='agent-full-access'){fs.writeFileSync(process.argv[1],m.params.modeId);console.log(JSON.stringify({jsonrpc:'2.0',id:m.id,result:{}}))}else console.log(JSON.stringify({jsonrpc:'2.0',id:m.id,error:{code:-32602,message:'wrong outer-sandbox mode'}}))});`
	rpc, err := Start(context.Background(), []string{"node", "-e", script, ack}, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rpc.Close(); err != nil {
			t.Error(err)
		}
	}()
	s := providerSession{ctx: context.Background(), rpc: rpc, input: Input{ACPProfile: "codex", ReadOnly: true, Execution: &execution.Profile{Launcher: "/app/bridges/nono/launch.mjs"}}}
	if err := s.setACPModes("native-session", []struct {
		ID string `json:"id"`
	}{{ID: "agent-full-access"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ack); err != nil {
		t.Fatalf("Codex never selected outer sandbox mode: %v", err)
	}
}
