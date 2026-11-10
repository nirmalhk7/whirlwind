package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func TestAgentNoticesRecordOriginatingRole(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	a := &agentAttempt{agentRun: &agentRun{Engine: e, ctx: ctx, project: project, task: task, input: provider.Input{Role: "review", CWD: cwd}}, id: "attempt", chosen: model.Provider{ID: "fake"}}
	if _, err := a.questions(ctx, []model.Question{{Text: "What should change?"}}); !errors.Is(err, ErrHumanDecisionPending) {
		t.Fatal(err)
	}
	if _, err := a.requestPermission(ctx, provider.Permission{Tool: "command", Command: []string{"git", "status"}}); !errors.Is(err, ErrHumanDecisionPending) {
		t.Fatal(err)
	}
	rows, err := e.Store.List(ctx, "inbox")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, row := range rows {
		var notice struct {
			Kind string `json:"kind"`
			Role string `json:"role"`
		}
		if err := json.Unmarshal(row.Data, &notice); err != nil {
			t.Fatal(err)
		}
		if notice.Kind == "question" || notice.Kind == "permission" {
			count++
			if notice.Role != "review" {
				t.Errorf("%s role = %q", notice.Kind, notice.Role)
			}
		}
	}
	if count != 2 {
		t.Fatalf("got %d agent notices", count)
	}
}
