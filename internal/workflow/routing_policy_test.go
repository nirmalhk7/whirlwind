package workflow

import (
	"testing"
	"whirlwind/internal/model"
)

func TestExplicitProviderPreferenceSurvivesRecovery(t *testing.T) {
	a := agentRun{project: model.Project{Policy: model.Policy{Providers: []string{"preferred", "fallback"}}}, task: model.Task{Recoveries: 2}, providers: []model.Provider{{ID: "fallback", Available: true}, {ID: "other", Available: true}}}
	got := a.preferences()
	if len(got) != 2 || got[0] != "preferred" || got[1] != "fallback" {
		t.Fatalf("explicit preference reordered on recovery: %v", got)
	}
}
