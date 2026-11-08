package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
)

func TestLiveOpenShellProcessAdapterACPHandshake(t *testing.T) {
	if os.Getenv("WHIRLWIND_OPENSHELL_LIVE_ACCEPTANCE") != "1" {
		t.Skip("set WHIRLWIND_OPENSHELL_LIVE_ACCEPTANCE=1 with the disposable local OpenShell gateway and fake ACP image")
	}
	worktree := t.TempDir()
	if output, err := exec.Command("git", "init", "--quiet", worktree).CombinedOutput(); err != nil {
		t.Fatalf("initialize disposable ACP worktree: %v: %s", err, output)
	}
	worktree, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := execution.ForPolicy(worktree, false, "live-acp-handshake", &model.SandboxPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	profile.AllowedPaths = []string{"**"}
	identity := fmt.Sprintf("acp-live-%d", time.Now().UnixNano())
	profile, err = execution.PinOpenShellRuntime(context.Background(), profile, identity, 1, time.Now().Add(2*time.Minute).UTC().Format(time.RFC3339Nano), filepath.Join(t.TempDir(), "supervisor"))
	if err != nil {
		t.Fatal(err)
	}
	adapter := ProcessAdapter{Provider: model.Provider{ID: "fake-acp", Name: "Credential-free ACP boundary probe", Kind: "acp", Command: []string{"codex-acp"}, Available: true}}
	thinking := &model.ThinkingChoice{ConfigID: "thinking", Value: "high"}
	output, err := adapter.Run(context.Background(), Input{
		Execution: &profile,
		CWD:       worktree,
		Role:      "code",
		Model:     "whirlwind-sandbox-probe-model",
		Thinking:  thinking,
		Prompt:    "Complete the credential-free sandbox handshake.",
	}, Hooks{})
	if err != nil {
		t.Fatalf("fake ACP handshake did not complete in OpenShell: %v", err)
	}
	if output.SessionID != "fake-openshell-session" || output.ResolvedModel != "whirlwind-sandbox-probe-model" || output.EffectiveThinking == nil || *output.EffectiveThinking != *thinking {
		t.Fatalf("ACP did not confirm the requested model/thinking settings: %+v", output)
	}
	assertOpenShellExecutionAbsent(t, profile)
}

func assertOpenShellExecutionAbsent(t *testing.T, profile execution.Profile) {
	t.Helper()
	cli := strings.TrimSpace(os.Getenv("WHIRLWIND_OPENSHELL_CLI"))
	if cli == "" {
		var err error
		cli, err = exec.LookPath("openshell")
		if err != nil {
			t.Fatalf("OpenShell CLI disappeared after the ACP handshake: %v", err)
		}
	}
	command := exec.Command(cli, "sandbox", "list", "--output", "json", "--gateway", profile.GatewayName, "--gateway-endpoint", profile.GatewayEndpoint, "--workspace", profile.Workspace)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("list pinned OpenShell workspace after ACP completion: %v", err)
	}
	var listing struct {
		Sandboxes []struct {
			ID     string            `json:"id"`
			Name   string            `json:"name"`
			Labels map[string]string `json:"labels"`
		} `json:"sandboxes"`
	}
	if err := json.Unmarshal(output, &listing); err != nil {
		t.Fatalf("OpenShell returned invalid sandbox list after ACP completion: %v", err)
	}
	for _, sandbox := range listing.Sandboxes {
		if sandbox.Name == profile.SandboxName || sandbox.Labels["whirlwind.io/execution"] == profile.ExecutionLabelValue {
			t.Fatalf("completed ACP execution still has sandbox %q in workspace %q", sandbox.ID, profile.Workspace)
		}
	}
}
