package provider

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"whirlwind/internal/model"
)

func TestNonoAgentLifecycleHooks(t *testing.T) {
	t.Setenv("WHIRLWIND_EXECUTION_LAUNCHER", "")
	t.Setenv("WHIRLWIND_DATA_DIR", t.TempDir())
	peer := filepath.Join(t.TempDir(), "agent.mjs")
	const source = `import fs from 'node:fs';
import readline from 'node:readline';
for await (const line of readline.createInterface({input: process.stdin})) {
  const m = JSON.parse(line);
  let result = {};
  switch (m.method) {
    case 'initialize': result = {protocolVersion:1, agentCapabilities:{loadSession:true}}; break;
    case 'session/new': result = {sessionId:'fixture-session'}; break;
    case 'session/load':
      if (m.params.sessionId !== 'fixture-session') throw Error('wrong session on repair');
      result = {sessionId:'fixture-session'}; break;
    case 'session/prompt':
      fs.appendFileSync('turns.txt', 'turn\n');
      if (!fs.existsSync('persistent') && m.params.prompt[0].text.includes('fixture-hook-failure')) {
        fs.writeFileSync('result.txt', 'correct\n');
      }
      console.log(JSON.stringify({jsonrpc:'2.0',method:'session/update',params:{update:{sessionUpdate:'agent_message_chunk',messageId:'final',content:{type:'text',text:'Done'}}}}));
      result = {stopReason:'end_turn'};
  }
  console.log(JSON.stringify({jsonrpc:'2.0',id:m.id,result}));
}`
	if err := os.WriteFile(peer, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	adapter := ProcessAdapter{Provider: model.Provider{ID: "fixture", Kind: "acp", Command: []string{"node", peer}}}
	for _, tt := range []struct {
		name       string
		before     bool
		persistent bool
		turns      int
		wantError  string
	}{
		{name: "before blocks provider", before: true, wantError: "before"},
		{name: "after repairs in same session", turns: 2},
		{name: "persistent after failure", persistent: true, turns: 4, wantError: "fixture-hook-failure"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cwd := hookFixtureRepo(t)
			if tt.persistent {
				if err := os.WriteFile(filepath.Join(cwd, "persistent"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			check := model.Check{Name: "candidate", Environment: map[string]string{"EXPECTED_CONTENT": "correct"}, Command: []string{
				"/bin/sh", "-c", `test "$EXPECTED_CONTENT" = correct || exit 2; grep -qx "$EXPECTED_CONTENT" result.txt || { echo fixture-hook-failure; exit 1; }`,
			}}
			hooks := model.AgentHookChecks{After: []model.Check{check}}
			if tt.before {
				hooks.Before = []model.Check{check}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			out, err := adapter.Run(ctx, Input{
				CWD: cwd, Role: "code", Prompt: "Implement the result", AgentHooks: hooks,
				Policy: model.Policy{AllowedPaths: []string{"**"}},
			}, Hooks{})
			if tt.wantError == "" {
				if err != nil || out.SessionID != "fixture-session" || out.Text != "Done" {
					t.Fatalf("expected accepted repair, got %+v / %v", out, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("expected blocked run with %q, got %v", tt.wantError, err)
			}
			turns, readErr := os.ReadFile(filepath.Join(cwd, "turns.txt"))
			if readErr != nil && !os.IsNotExist(readErr) {
				t.Fatal(readErr)
			}
			if got := strings.Count(string(turns), "turn\n"); got != tt.turns {
				t.Fatalf("ran %d agent turns, want %d", got, tt.turns)
			}
		})
	}
}

func TestNonoAgentHookDefersFinalization(t *testing.T) {
	t.Setenv("WHIRLWIND_EXECUTION_LAUNCHER", "")
	t.Setenv("WHIRLWIND_DATA_DIR", t.TempDir())
	peer := filepath.Join(t.TempDir(), "finalizer.mjs")
	const source = `import {spawnSync} from 'node:child_process';
import readline from 'node:readline';
let server;
for await (const line of readline.createInterface({input: process.stdin})) {
  const m = JSON.parse(line);
  let result = {};
  if (m.method === 'initialize') result = {protocolVersion:1,agentCapabilities:{loadSession:true}};
  if (m.method === 'session/new' || m.method === 'session/load') {
    server = m.params.mcpServers.find(server => server.name === 'whirlwind_checks');
    result = {sessionId:'finalizer-session'};
  }
  if (m.method === 'session/prompt') {
    const tool = {jsonrpc:'2.0',id:1,method:'tools/call',params:{name:'finalize_delivery',arguments:{}}};
    const reply = spawnSync(server.command, server.args, {encoding:'utf8',input:JSON.stringify(tool)+'\n',timeout:10000});
    if (reply.status !== 0) throw Error(reply.stderr);
    const receipt = JSON.parse(JSON.parse(reply.stdout).result.content[0].text);
    const text = receipt.pending ? 'Delivery requested' : 'Delivery already published';
    console.log(JSON.stringify({jsonrpc:'2.0',method:'session/update',params:{update:{sessionUpdate:'agent_message_chunk',content:{type:'text',text}}}}));
    result = {stopReason:'end_turn'};
  }
  console.log(JSON.stringify({jsonrpc:'2.0',id:m.id,result}));
}`
	if err := os.WriteFile(peer, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	adapter := ProcessAdapter{Provider: model.Provider{ID: "fixture", Kind: "acp", Command: []string{"node", peer}}}
	for _, tt := range []struct {
		name, check string
		published   int32
	}{
		{"persistent failure never publishes", "/usr/bin/false", 0},
		{"passing after hook publishes once", "/usr/bin/true", 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cwd := hookFixtureRepo(t)
			var published atomic.Int32
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			out, err := adapter.Run(ctx, Input{
				CWD: cwd, Role: "finalizer", Purpose: "local_finalization", ReadOnly: true, Prompt: "Request local delivery",
				AgentHooks: model.AgentHookChecks{After: []model.Check{{Name: "delivery", Command: []string{tt.check}}}},
			}, Hooks{FinalizeDelivery: func(context.Context) (FinalizationResult, error) {
				published.Add(1)
				return FinalizationResult{Branch: "fixture-delivery"}, nil
			}})
			if tt.published == 0 {
				if err == nil || !strings.Contains(err.Error(), "sandbox after hook failed") {
					t.Fatalf("expected sandbox block, got %v", err)
				}
			} else if err != nil || out.Text != "Delivery requested" {
				t.Fatalf("expected pending receipt followed by successful delivery, got %+v / %v", out, err)
			}
			if count := published.Load(); count != tt.published {
				t.Fatalf("published %d times, want %d", count, tt.published)
			}
		})
	}
}

func hookFixtureRepo(t *testing.T) string {
	t.Helper()
	cwd := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-q", "--allow-empty", "-m", "base"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = cwd
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	return cwd
}
