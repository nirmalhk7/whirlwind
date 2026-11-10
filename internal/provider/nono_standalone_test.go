package provider

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
	"whirlwind/internal/model"
)

// Direct adapter callers also need the resolved outer profile for mode negotiation.
func TestStandaloneCodexUsesResolvedNonoProfile(t *testing.T) {
	t.Setenv("WHIRLWIND_EXECUTION_LAUNCHER", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WHIRLWIND_DATA_DIR", t.TempDir())
	if err := os.Mkdir(filepath.Join(home, ".codex"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "auth.json"), []byte("synthetic-standalone-auth"), 0600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "agent.mjs")
	source := `import readline from 'node:readline';let configured=false;const send=x=>console.log(JSON.stringify(x));readline.createInterface({input:process.stdin}).on('line',line=>{const m=JSON.parse(line);let result={};if(m.method==='initialize')result={protocolVersion:1,agentCapabilities:{}};if(m.method==='session/new')result={sessionId:'outer-profile',modes:{availableModes:[{id:'agent-full-access'}]}};if(m.method==='session/set_mode')configured=m.params.modeId==='agent-full-access';if(m.method==='session/prompt'){if(!configured){send({jsonrpc:'2.0',id:m.id,error:{code:-32602,message:'outer mode missing'}});return}send({jsonrpc:'2.0',method:'session/update',params:{update:{sessionUpdate:'agent_message_chunk',messageId:'final',content:{type:'text',text:'outer-configured'}}}});result={stopReason:'end_turn'}}send({jsonrpc:'2.0',id:m.id,result})});`
	if err := os.WriteFile(script, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	adapter := ProcessAdapter{Provider: model.Provider{ID: "codex", Kind: "acp", ACPProfile: "codex", Command: []string{"node", script}}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := adapter.Run(ctx, Input{CWD: t.TempDir(), ReadOnly: true, Prompt: "respond"}, Hooks{})
	if err != nil || out.Text != "outer-configured" {
		t.Fatalf("standalone native round trip: %+v %v", out, err)
	}
}
