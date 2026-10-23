package workflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

func TestTDDCannotStartRedAfterProductionAlreadyChanged(t *testing.T) {
	exit := 1
	result := verification.Result{Name: "tests", Command: []string{"go", "test", "-json", "./..."}, ExitCode: &exit, Output: "{\"Action\":\"fail\",\"Package\":\"sample\",\"Test\":\"TestRegression\"}\n{\"Action\":\"fail\",\"Package\":\"sample\"}\n"}
	origin := codingOrigin{Production: "before", Tests: "old-tests"}
	if phase := tddPhase(origin, "already-edited", "new-tests", result, true); phase == "red" {
		t.Fatal("post-implementation test accepted as TDD red")
	}
	if phase := tddPhase(origin, "before", "new-tests", result, true); phase != "red" {
		t.Fatalf("valid test-first regression rejected: %s", phase)
	}
	if phase := tddPhase(origin, "before", "old-tests", result, true); phase == "red" {
		t.Fatal("unchanged existing failure accepted as new red")
	}
}

func tddGit(t *testing.T, path string, args ...string) string {
	t.Helper()
	out, err := github.Run(context.Background(), path, "git", args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func tddWrite(t *testing.T, path, name, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(path, name), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func tddCommit(t *testing.T, path string) string {
	t.Helper()
	tddGit(t, path, "add", ".")
	tddGit(t, path, "commit", "-m", "fixture")
	return tddGit(t, path, "rev-parse", "HEAD")
}

func tddEvidenceFixture(t *testing.T) (*Engine, model.Task, string) {
	t.Helper()
	e, task, path := testEngine(t)
	tddWrite(t, path, "one.go", "package sample\nvar One = false\n")
	tddWrite(t, path, "two.go", "package sample\nvar Two = false\n")
	task.Base = tddCommit(t, path)
	task.Worktree = path
	task.Plan = &model.Plan{Units: []model.Unit{{ID: "one", Base: task.Base}}}
	return e, task, path
}

func tddSaveCheck(t *testing.T, e *Engine, task model.Task, path, unit, phase string) {
	t.Helper()
	snapshot, production, tests, files, err := codingSnapshotManifest(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	check := tddCheck{Task: task.ID, PlanVersion: task.PlanVersion, Unit: unit, CheckID: "test", Phase: phase, Passed: phase == "green", Snapshot: snapshot, Production: production, Tests: tests, Files: files, At: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := e.Store.Put(context.Background(), "tdd_checks", store.ID(), check, 0); err != nil {
		t.Fatal(err)
	}
}

func tddCompleteOne(t *testing.T, e *Engine, task *model.Task, path string) {
	t.Helper()
	tddWrite(t, path, "one_test.go", "package sample\n// one regression\n")
	tddSaveCheck(t, e, *task, path, "one", "red")
	tddWrite(t, path, "one.go", "package sample\nvar One = true\n")
	tddSaveCheck(t, e, *task, path, "one", "green")
	task.Plan.Units[0].Commit = tddCommit(t, path)
}

func TestTDDEvidenceRejectsProductionOrTestsEditedAfterGreen(t *testing.T) {
	for _, file := range []string{"one.go", "one_test.go"} {
		t.Run(file, func(t *testing.T) {
			e, task, path := tddEvidenceFixture(t)
			tddCompleteOne(t, e, &task, path)
			if err := e.requireTDDEvidence(context.Background(), task); err != nil {
				t.Fatal(err)
			}
			tddWrite(t, path, file, "package sample\n// changed after green\n")
			tddCommit(t, path)
			if err := e.requireTDDEvidence(context.Background(), task); err == nil {
				t.Fatal("stale green authorized later edits")
			}
		})
	}
}

func TestTDDEvidenceCannotReuseAnotherUnitsPair(t *testing.T) {
	e, task, path := tddEvidenceFixture(t)
	tddCompleteOne(t, e, &task, path)
	task.Plan.Units = append(task.Plan.Units, model.Unit{ID: "other", Base: task.Base, Commit: task.Plan.Units[0].Commit})
	if err := e.requireTDDEvidence(context.Background(), task); err == nil {
		t.Fatal("other executable unit borrowed first unit's red/green")
	}
}

func TestTDDEvidenceCoversIndependentParallelUnitSnapshots(t *testing.T) {
	e, task, path := tddEvidenceFixture(t)
	tddCompleteOne(t, e, &task, path)
	tddGit(t, path, "checkout", "-b", "unit-two", task.Base)
	tddWrite(t, path, "two_test.go", "package sample\n// two regression\n")
	tddSaveCheck(t, e, task, path, "two", "red")
	tddWrite(t, path, "two.go", "package sample\nvar Two = true\n")
	tddSaveCheck(t, e, task, path, "two", "green")
	second := tddCommit(t, path)
	tddGit(t, path, "checkout", "main")
	tddGit(t, path, "cherry-pick", second)
	task.Plan.Units = append(task.Plan.Units, model.Unit{ID: "two", Base: task.Base, Commit: second})
	if err := e.requireTDDEvidence(context.Background(), task); err != nil {
		t.Fatalf("independent complete coverage rejected: %v", err)
	}
	// Unit two's complete snapshot must not bless an unrelated post-green edit.
	tddWrite(t, path, "one.go", "package sample\nvar One = 99\n")
	tddCommit(t, path)
	if err := e.requireTDDEvidence(context.Background(), task); err == nil {
		t.Fatal("parallel evidence authorized later source edit")
	}
}

func TestTDDEvidenceTextOnlyChangesRemainExempt(t *testing.T) {
	e, task, path := tddEvidenceFixture(t)
	tddWrite(t, path, "notes.md", "Updated documentation\n")
	tddCommit(t, path)
	if err := e.requireTDDEvidence(context.Background(), task); err != nil {
		t.Fatalf("text-only change required TDD: %v", err)
	}
}

func TestTDDEvidenceUnrelatedPairCannotCoverAnEarlierUntestedEdit(t *testing.T) {
	e, task, path := tddEvidenceFixture(t)
	tddCompleteOne(t, e, &task, path)
	first := task.Plan.Units[0].Commit
	tddWrite(t, path, "one.go", "package sample\nvar One = 99\n")
	tddWrite(t, path, "two_test.go", "package sample\n// two regression\n")
	tddSaveCheck(t, e, task, path, "two", "red")
	tddWrite(t, path, "two.go", "package sample\nvar Two = true\n")
	tddSaveCheck(t, e, task, path, "two", "green")
	second := tddCommit(t, path)
	task.Plan.Units = append(task.Plan.Units, model.Unit{ID: "two", Base: first, Commit: second})
	if err := e.requireTDDEvidence(context.Background(), task); err == nil {
		t.Fatal("later unrelated green laundered production already changed before its red")
	}
}

func TestTDDEvidenceRejectsExecutableModeChangesAfterGreen(t *testing.T) {
	e, task, path := tddEvidenceFixture(t)
	tddCompleteOne(t, e, &task, path)
	if err := os.Chmod(filepath.Join(path, "one.go"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := e.requireTDDEvidence(context.Background(), task); err == nil {
		t.Fatal("late executable-mode edit reused old green snapshot")
	}
}

func TestTDDEvidenceLegacyAggregateHashesDoNotAuthorizeSource(t *testing.T) {
	e, task, path := tddEvidenceFixture(t)
	tddCompleteOne(t, e, &task, path)
	rows, err := e.Store.List(context.Background(), "tdd_checks")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		check, err := store.Decode[tddCheck](row)
		if err != nil {
			t.Fatal(err)
		}
		check.Files = nil
		if err := e.Store.Put(context.Background(), "tdd_checks", row.ID, check, row.Version); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.requireTDDEvidence(context.Background(), task); err == nil {
		t.Fatal("legacy pair without per-file coverage authorized source")
	}
}
