package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"whirlwind/internal/model"
)

func TestInvalidUnitScopeFailsBeforeWorkspaceCreation(t *testing.T) {
	for _, scope := range []string{"tests/**", "src/__tests__/", "src/**/overpass*.test.ts", "src/[bad"} {
		t.Run(scope, func(t *testing.T) {
			e, task, _ := testEngine(t)
			p, _ := e.Project(context.Background(), task.ProjectID)
			p.Policy.AllowedPaths = []string{"src/**"}
			task.Plan = &model.Plan{Units: []model.Unit{{ID: "good", AllowedPaths: []string{"src/ok.ts"}}, {ID: "invalid", AllowedPaths: []string{scope}}}}
			err := e.code(context.Background(), p, &task)
			if err == nil || !strings.Contains(err.Error(), scope) || !strings.Contains(err.Error(), "invalid") {
				t.Fatalf("missing actionable scope rejection: %v", err)
			}
			if task.Worktree != "" {
				t.Fatalf("invalid plan created workspace %s", task.Worktree)
			}
			if _, err = os.Stat(filepath.Join(e.Root, "worktrees", task.ID)); !os.IsNotExist(err) {
				t.Fatalf("workspace side effect: %v", err)
			}
		})
	}
}

func TestUnitGlobCannotWidenProjectGlob(t *testing.T) {
	p := model.Project{Policy: model.Policy{AllowedPaths: []string{"src/?.ts"}}}
	if err := ValidateUnitScopes(p, []model.Unit{{ID: "wide", AllowedPaths: []string{"src/*.ts"}}}); err == nil {
		t.Fatal("unit glob escaped narrower project glob")
	}
	if err := ValidateUnitScopes(p, []model.Unit{{ID: "exact", AllowedPaths: []string{"src/a.ts"}}}); err != nil {
		t.Fatalf("valid concrete path rejected: %v", err)
	}
}
