package workflow

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func TestLearningLateExactSourceMatchSurvivesLongIssue(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	hash, _ := learningFingerprint(cwd, learningPolicyFixturePath)
	l := model.Lesson{ID: "source-lesson", ProjectID: p.ID, Role: "plan", Status: "tentative", Trigger: "nebula", Mistake: "oldapproach", Correction: "Respect the schema sentinel", Paths: []string{learningPolicyFixturePath}, Anchors: map[string]string{learningPolicyFixturePath: hash}}
	if err := e.Store.Put(ctx, "lessons", l.ID, l, 0); err != nil {
		t.Fatal(err)
	}
	task.Issue.Title = "unrelated"
	task.Issue.Body = ""
	for i := 0; i < 60; i++ {
		task.Issue.Body += fmt.Sprintf(" unrelatedword%d", i)
	}
	task.Issue.Body += " " + learningPolicyFixturePath
	var prompt string
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
			prompt = in.Prompt
			return provider.Output{Text: "done"}, nil
		})
	}
	if _, err := e.agent(ctx, p, task, provider.Input{Role: "plan", CWD: cwd, Prompt: "inspect issue source"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "Respect the schema sentinel") {
		t.Fatal("late exact source match was truncated before lookup")
	}
}
