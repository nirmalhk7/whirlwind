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
	source := learningEvaluationProvider(t)
	for _, role := range model.AgentRoles() {
		for _, taught := range []bool{false, true} {
			t.Run(role+map[bool]string{false: "/baseline", true: "/learned"}[taught], func(t *testing.T) {
				runLearningEvaluation(t, source, role, taught)
			})
		}
	}
}

func learningEvaluationProvider(t *testing.T) model.Provider {
	t.Helper()
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
	return model.Provider{ID: "eval", Kind: kind, Command: command, Available: true, Capabilities: model.AgentRoles()}
}

func runLearningEvaluation(t *testing.T, source model.Provider, role string, taught bool) {
	t.Helper()
	e, task, cwd := testEngine(t)
	task.Issue.Title = "project policy check"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	p, _ := e.Project(ctx, task.ProjectID)
	configureLearningEvaluation(t, ctx, e, source, role)
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
	attempts, _ := e.Store.List(ctx, "sessions")
	row, memoryBytes := learningEvaluationRow(role, taught, out, attempts, started)
	b, _ := json.Marshal(row)
	t.Log(string(b))
	if taught && memoryBytes == 0 {
		t.Error("teaching turn did not produce recalled learning")
	}
}

func configureLearningEvaluation(t *testing.T, ctx context.Context, e *Engine, source model.Provider, role string) {
	t.Helper()
	if err := e.Store.Put(ctx, "providers", source.ID, source, 0); err != nil {
		t.Fatal(err)
	}
	config := model.AgentConfig{Role: role, Chain: []model.AgentChoice{{Provider: source.ID, Model: os.Getenv("WHIRLWIND_LEARNING_EVAL_MODEL")}}}
	if err := e.Store.Put(ctx, "agents", role, config, 0); err != nil {
		t.Fatal(err)
	}
	e.Adapter = func(p model.Provider) provider.Adapter { return provider.ProcessAdapter{Provider: p} }
}

func learningEvaluationRow(role string, taught bool, out provider.Output, attempts []model.Record, started time.Time) (map[string]any, int) {
	var answer struct {
		Check string `json:"check"`
	}
	_ = json.Unmarshal([]byte(out.Text), &answer)
	totals := summarizeLearningEvaluation(attempts, out.AttemptID)
	row := map[string]any{"role": role, "learned": taught, "accepted": strings.TrimSpace(answer.Check) == "test -f result.txt", "attempts": len(attempts), "elapsed_ms": time.Since(started).Milliseconds(), "memory_bytes": totals.memoryBytes, "reported_usage": totals.usages, "total_tokens": nil, "total_cost": nil}
	if totals.tokensKnown {
		row["total_tokens"] = totals.tokens
	}
	if totals.costKnown {
		row["total_cost"] = totals.cost
	}
	return row, totals.memoryBytes
}

type learningEvaluationTotals struct {
	usages                 []*model.Usage
	memoryBytes            int
	tokens                 int64
	cost                   float64
	tokensKnown, costKnown bool
}

func summarizeLearningEvaluation(attempts []model.Record, queryAttempt string) learningEvaluationTotals {
	totals := learningEvaluationTotals{usages: []*model.Usage{}, tokensKnown: true, costKnown: true}
	for _, record := range attempts {
		attempt, _ := store.Decode[model.Attempt](record)
		totals.add(attempt.Usage)
		if attempt.AttemptID == queryAttempt && attempt.Learning != nil {
			totals.memoryBytes = attempt.Learning.Bytes
		}
	}
	return totals
}

func (totals *learningEvaluationTotals) add(usage *model.Usage) {
	totals.usages = append(totals.usages, usage)
	if usage == nil || usage.Scope != "attempt" || usage.TotalTokens == nil {
		totals.tokensKnown = false
	} else {
		totals.tokens += *usage.TotalTokens
	}
	if usage == nil || usage.Scope != "attempt" || usage.Cost == nil {
		totals.costKnown = false
	} else {
		totals.cost += *usage.Cost
	}
}
