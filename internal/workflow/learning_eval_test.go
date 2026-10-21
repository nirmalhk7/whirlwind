package workflow

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

// TestLearningEvaluation is opt-in because it invokes an authenticated model.
// It measures controlled role probes, not end-to-end software delivery quality.
func TestLearningEvaluation(t *testing.T) {
	if os.Getenv("WHIRLWIND_LEARNING_EVAL") != "1" {
		t.Skip("set WHIRLWIND_LEARNING_EVAL=1 to run authenticated paired role probes")
	}
	kind := os.Getenv("WHIRLWIND_LEARNING_EVAL_KIND")
	if kind == "" {
		kind = "codex"
	}
	command := []string{"codex", "app-server", "--listen", "stdio://"}
	if raw := os.Getenv("WHIRLWIND_LEARNING_EVAL_COMMAND"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &command); err != nil {
			t.Fatal(err)
		}
	}
	if len(command) == 0 || (kind != "codex" && kind != "acp" && kind != "claude") {
		t.Fatal("supported kind and nonempty command required")
	}
	for _, role := range model.AgentRoles() {
		for _, taught := range []bool{false, true} {
			t.Run(role+map[bool]string{false: "/baseline", true: "/learned"}[taught], func(t *testing.T) {
				e, task, cwd := testEngine(t)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				defer cancel()
				p, _ := e.Project(ctx, task.ProjectID)
				source := model.Provider{ID: "eval", Kind: kind, Command: command, Available: true, Capabilities: model.AgentRoles()}
				if err := e.Store.Put(ctx, "providers", source.ID, source, 0); err != nil {
					t.Fatal(err)
				}
				config := model.AgentConfig{Role: role, Chain: []model.AgentChoice{{Provider: source.ID, Model: os.Getenv("WHIRLWIND_LEARNING_EVAL_MODEL")}}}
				if err := e.Store.Put(ctx, "agents", role, config, 0); err != nil {
					t.Fatal(err)
				}
				e.Adapter = func(p model.Provider) provider.Adapter { return provider.ProcessAdapter{Provider: p} }
				started := time.Now()
				if taught {
					_, err := e.agent(ctx, p, task, provider.Input{Role: role, CWD: cwd, ReadOnly: true, Prompt: `Prior corrected mistake: guessing that this project uses go test for verification. The committed .whirlwind.yaml defines its check as sh -c 'test -f result.txt'. Record one compact lesson about reading this project's policy instead of guessing check commands, anchored to .whirlwind.yaml. Do not run commands or modify files. Return result "recorded" with learning.`})
					if err != nil {
						t.Fatal(err)
					}
				}
				task.ID = "evaluation-task"
				out, err := e.agent(ctx, p, task, provider.Input{Role: role, CWD: cwd, ReadOnly: true, Prompt: `For this project policy, which shell check verifies the required output? Do not run commands, modify files, or submit new learning during this evaluation. Return result {"check":"the exact shell command"}.`})
				if err != nil {
					t.Fatal(err)
				}
				var answer struct {
					Check string `json:"check"`
				}
				_ = json.Unmarshal([]byte(out.Text), &answer)
				attempts, _ := e.Store.List(ctx, "sessions")
				usages := []*model.Usage{}
				memoryBytes := 0
				var tokens int64
				costKnown, tokensKnown := true, true
				totalCost := 0.0
				for _, r := range attempts {
					a, _ := store.Decode[model.Attempt](r)
					usages = append(usages, a.Usage)
					if a.Usage == nil || a.Usage.Scope != "attempt" || a.Usage.TotalTokens == nil {
						tokensKnown = false
					} else {
						tokens += *a.Usage.TotalTokens
					}
					if a.Usage == nil || a.Usage.Scope != "attempt" || a.Usage.Cost == nil {
						costKnown = false
					} else {
						totalCost += *a.Usage.Cost
					}
					if a.AttemptID == out.AttemptID && a.Learning != nil {
						memoryBytes = a.Learning.Bytes
					}
				}
				row := map[string]any{"role": role, "learned": taught, "accepted": strings.TrimSpace(answer.Check) == "test -f result.txt", "attempts": len(attempts), "elapsed_ms": time.Since(started).Milliseconds(), "memory_bytes": memoryBytes, "reported_usage": usages, "total_tokens": nil, "total_cost": nil}
				if tokensKnown {
					row["total_tokens"] = tokens
				}
				if costKnown {
					row["total_cost"] = totalCost
				}
				b, _ := json.Marshal(row)
				t.Log(string(b))
				if taught && memoryBytes == 0 {
					t.Error("teaching turn did not produce recalled learning")
				}
			})
		}
	}
}
