package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func TestLearningBoundsRecallAndInvalidatesChangedSources(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	hash, err := learningFingerprint(cwd, ".whirlwind.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 160; i++ {
		l := model.Lesson{ID: fmt.Sprint(i), ProjectID: p.ID, Role: "code", Status: "tentative", Trigger: "policy change", Mistake: "guessing checks", Correction: strings.Repeat("Read policy. ", 30), Paths: []string{".whirlwind.yaml"}, Anchors: map[string]string{".whirlwind.yaml": hash}}
		if err = e.Store.Put(ctx, "lessons", l.ID, l, 0); err != nil {
			t.Fatal(err)
		}
	}
	var prompt string
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
			prompt = in.Prompt
			return provider.Output{Text: "done"}, nil
		})
	}
	out, err := e.agent(ctx, p, task, provider.Input{Role: "code", CWD: cwd, Prompt: "policy change"})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := e.Store.Get(ctx, "sessions", out.AttemptID)
	a, _ := store.Decode[model.Attempt](r)
	if a.Learning == nil || len(a.Learning.Recalled) == 0 || len(a.Learning.Recalled) > 3 || a.Learning.Bytes > 2048 {
		t.Fatalf("unbounded memory: %+v", a.Learning)
	}
	if strings.Count(prompt, `"avoid":"guessing checks"`) > 3 {
		t.Fatal("history leaked into prompt")
	}
	if err = os.WriteFile(filepath.Join(cwd, ".whirlwind.yaml"), []byte("changed policy\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = e.agent(ctx, p, task, provider.Input{Role: "code", CWD: cwd, Prompt: "policy change"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, "Read policy.") {
		t.Fatal("stale lesson recalled")
	}
	records, _ := e.Store.List(ctx, "lessons")
	stale := 0
	for _, r := range records {
		var l model.Lesson
		_ = json.Unmarshal(r.Data, &l)
		if l.Status == "stale" {
			stale++
		}
	}
	if stale == 0 {
		t.Fatal("staleness not persisted")
	}
}

func TestLearningRejectsInventedEvidenceAndEscapingPaths(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(cwd, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../outside", "escape", ".git/config"} {
		e.Adapter = func(model.Provider) provider.Adapter {
			return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
				return provider.Output{Text: fmt.Sprintf(`{"result":"done","learning":{"lessons":[{"trigger":"policy","mistake":"guessing","correction":"read","paths":[%q]}]}}`, path)}, nil
			})
		}
		if _, err := e.agent(ctx, p, task, provider.Input{Role: "code", CWD: cwd, Prompt: "policy"}); err != nil {
			t.Fatal(err)
		}
	}
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			return provider.Output{Text: `{"result":"done","learning":{"lessons":[{"trigger":"policy","mistake":"guessing","correction":"read","paths":[".whirlwind.yaml"],"evidence_ids":["fabricated"]}]}}`}, nil
		})
	}
	if _, err := e.agent(ctx, p, task, provider.Input{Role: "code", CWD: cwd, Prompt: "policy"}); err != nil {
		t.Fatal(err)
	}
	records, _ := e.Store.List(ctx, "lessons")
	if len(records) != 0 {
		t.Fatalf("unsafe lessons stored: %d", len(records))
	}
}

func TestLearningEvidenceOnlyRecallAndFeedback(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	origin := model.Attempt{AttemptID: "origin", ProjectID: p.ID, Task: task.ID, Role: "code"}
	if err := e.Store.Put(ctx, "sessions", origin.AttemptID, origin, 0); err != nil {
		t.Fatal(err)
	}
	evidence, err := e.Store.Capture(ctx, origin.AttemptID, "check:unit", "text/plain", []byte("schema regression reproduced"), 1024, false)
	if err != nil {
		t.Fatal(err)
	}
	l := model.Lesson{Trigger: "schema mismatch", Mistake: "editing generated output", Correction: "Edit the schema", EvidenceIDs: []string{evidence.ID}}
	snapshot, err := e.Store.CommitLearning(ctx, origin, model.LearningReport{Lessons: []model.Lesson{l}})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := e.Store.CommitLearning(ctx, origin, model.LearningReport{Lessons: []model.Lesson{l}})
	if err != nil || len(replay.CreatedIDs) != 1 || replay.CreatedIDs[0] != snapshot.CreatedIDs[0] {
		t.Fatalf("report replay: %+v %v", replay, err)
	}
	var prompt string
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
			prompt = in.Prompt
			return provider.Output{Text: "done"}, nil
		})
	}
	task.ID = "next-task"
	if _, err = e.agent(ctx, p, task, provider.Input{Role: "code", CWD: cwd, Prompt: "schema mismatch"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "Edit the schema") {
		t.Fatal("evidence-only tentative lesson lost across tasks")
	}
	third := origin
	third.AttemptID = "reused-evidence"
	third.Learning = &model.LearningSnapshot{Recalled: []model.LessonRef{{ID: snapshot.CreatedIDs[0], Version: 1, Status: "tentative"}}}
	if err = e.Store.Put(ctx, "sessions", third.AttemptID, third, 0); err != nil {
		t.Fatal(err)
	}
	oldFeedback := model.LessonFeedback{LessonID: snapshot.CreatedIDs[0], Verdict: "supports", Reason: "same observation", EvidenceIDs: []string{evidence.ID}}
	oldResult, err := e.Store.CommitLearning(ctx, third, model.LearningReport{Feedback: []model.LessonFeedback{oldFeedback}})
	if err != nil || len(oldResult.Feedback) != 0 {
		t.Fatalf("old evidence corroborated itself: %+v %v", oldResult, err)
	}
	// A same-role independent tool/check observation can corroborate, but a final reply cannot.
	another := origin
	another.AttemptID = "another"
	another.Learning = &model.LearningSnapshot{Recalled: []model.LessonRef{{ID: snapshot.CreatedIDs[0], Version: 1, Status: "tentative"}}}
	if err = e.Store.Put(ctx, "sessions", another.AttemptID, another, 0); err != nil {
		t.Fatal(err)
	}
	raw, err := e.Store.Capture(ctx, another.AttemptID, "final_output", "text/plain", []byte("I agree"), 1024, false)
	if err != nil {
		t.Fatal(err)
	}
	feedback := model.LessonFeedback{LessonID: snapshot.CreatedIDs[0], Verdict: "supports", Reason: "assertion", EvidenceIDs: []string{raw.ID}}
	got, err := e.Store.CommitLearning(ctx, another, model.LearningReport{Feedback: []model.LessonFeedback{feedback}})
	if err != nil || len(got.Feedback) != 0 {
		t.Fatalf("self assertion accepted: %+v %v", got, err)
	}
}

func TestLearningRecentSpecificLessonSurvivesLargeHistory(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	hash, _ := learningFingerprint(cwd, ".whirlwind.yaml")
	for i := 0; i < 140; i++ {
		l := model.Lesson{ID: fmt.Sprint(i), ProjectID: p.ID, Role: "plan", Status: "tentative", Trigger: "policy", Mistake: "guessing", Correction: "Read general docs", Anchors: map[string]string{".whirlwind.yaml": hash}}
		if i == 139 {
			l.Paths = []string{".whirlwind.yaml"}
			l.Correction = "Specific policy correction"
		}
		if err := e.Store.Put(ctx, "lessons", l.ID, l, 0); err != nil {
			t.Fatal(err)
		}
	}
	var prompt string
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
			prompt = in.Prompt
			return provider.Output{Text: "done"}, nil
		})
	}
	if _, err := e.agent(ctx, p, task, provider.Input{Role: "plan", CWD: cwd, Prompt: "policy .whirlwind.yaml"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "Specific policy correction") {
		t.Fatal("older generic lessons starved precise lesson")
	}
}

func TestLearningProviderFailureDoesNotCreateLessons(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			return provider.Output{Text: `{"result":"done","learning":{"lessons":[{"trigger":"policy","mistake":"guessing","correction":"read","paths":[".whirlwind.yaml"]}]}}`}, fmt.Errorf("transport interrupted")
		})
	}
	_, err := e.agent(ctx, p, task, provider.Input{Role: "code", CWD: cwd, Prompt: "policy"})
	if err == nil {
		t.Fatal("provider failure lost")
	}
	records, _ := e.Store.List(ctx, "lessons")
	if len(records) != 0 {
		t.Fatal("partial reply taught a lesson")
	}
}

func TestLearningFeedbackCanUseObservedSource(t *testing.T) {
	e, task, cwd := testEngine(t)
	ctx := context.Background()
	p, _ := e.Project(ctx, task.ProjectID)
	var id string
	step := 0
	e.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(_ context.Context, in provider.Input, _ provider.Hooks) (provider.Output, error) {
			step++
			if step == 1 {
				return provider.Output{Text: `{"result":"done","learning":{"lessons":[{"trigger":"policy","mistake":"guessing","correction":"Read policy","paths":[".whirlwind.yaml"]}]}}`}, nil
			}
			return provider.Output{Text: fmt.Sprintf(`{"result":"done","learning":{"feedback":[{"lesson_id":%q,"verdict":"supports","reason":"Confirmed the committed policy","paths":[".whirlwind.yaml"]}]}}`, id)}, nil
		})
	}
	if _, err := e.agent(ctx, p, task, provider.Input{Role: "code", CWD: cwd, Prompt: "policy"}); err != nil {
		t.Fatal(err)
	}
	records, _ := e.Store.List(ctx, "lessons")
	id = records[0].ID
	if _, err := e.agent(ctx, p, task, provider.Input{Role: "code", CWD: cwd, Prompt: "policy"}); err != nil {
		t.Fatal(err)
	}
	r, _ := e.Store.Get(ctx, "lessons", id)
	l, _ := store.Decode[model.Lesson](r)
	if l.Status != "corroborated" || len(l.EvidenceIDs) != 1 {
		t.Fatalf("source feedback not applied: %s", r.Data)
	}
}
