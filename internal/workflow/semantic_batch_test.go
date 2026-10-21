package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

const batchSemanticQuery = "retry duplication"
const firstBatchLesson = "lesson-0"

type embeddingRequestCounts struct {
	mu    sync.Mutex
	sizes []int
}

func (counts *embeddingRequestCounts) record(size int) {
	counts.mu.Lock()
	defer counts.mu.Unlock()
	counts.sizes = append(counts.sizes, size)
}

func (counts *embeddingRequestCounts) assert(t *testing.T, want int) {
	t.Helper()
	counts.mu.Lock()
	defer counts.mu.Unlock()
	if len(counts.sizes) != want {
		t.Fatalf("embedding requests: got %d want %d", len(counts.sizes), want)
	}
	for _, size := range counts.sizes {
		if size > 32 {
			t.Fatalf("unbounded embedding request: %d inputs", size)
		}
	}
}

func batchEmbeddingRun(t *testing.T, lessons int) (*agentRun, *embeddingRequestCounts) {
	t.Helper()
	engine, task, cwd := testEngine(t)
	ctx := context.Background()
	project, err := engine.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	counts := &embeddingRequestCounts{}
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		counts.record(len(request.Input))
		data := []any{}
		for i := range request.Input {
			data = append(data, map[string]any{"index": i, "embedding": []float64{1, 0}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(endpoint.Close)
	project.Policy.Embeddings = &model.Embeddings{Endpoint: endpoint.URL, Model: "batch-model", LessonsEnabled: true}
	for i := 0; i < lessons; i++ {
		lesson := model.Lesson{ID: fmt.Sprintf("lesson-%d", i), ProjectID: project.ID, Role: "code", Status: "tentative", Trigger: "idempotency", Mistake: "duplicate writes", Correction: "use a stable key", EvidenceIDs: []string{"observed-check"}}
		if err := engine.Store.Put(ctx, "lessons", lesson.ID, lesson, 0); err != nil {
			t.Fatal(err)
		}
	}
	return &agentRun{Engine: engine, ctx: ctx, project: project, task: task, input: provider.Input{Role: "code", CWD: cwd}}, counts
}

func assertSemanticBatchSearch(t *testing.T, run *agentRun, counts *embeddingRequestCounts, want int) {
	t.Helper()
	if _, err := run.semanticMatches(run.ctx, batchSemanticQuery); err != nil {
		t.Fatal(err)
	}
	counts.assert(t, want)
}

func TestSemanticEmbeddingsBatchAndReuseCache(t *testing.T) {
	run, counts := batchEmbeddingRun(t, 65)
	assertSemanticBatchSearch(t, run, counts, 4)
	assertSemanticBatchSearch(t, run, counts, 4)
}

func TestSemanticEmbeddingsInvalidateChangedLessonAndConfig(t *testing.T) {
	run, counts := batchEmbeddingRun(t, 3)
	assertSemanticBatchSearch(t, run, counts, 2)
	record, err := run.Store.Get(run.ctx, "lessons", firstBatchLesson)
	if err != nil {
		t.Fatal(err)
	}
	lesson, err := store.Decode[model.Lesson](record)
	if err != nil {
		t.Fatal(err)
	}
	lesson.Correction = "use a transaction plus stable key"
	if err := run.Store.Put(run.ctx, "lessons", record.ID, lesson, record.Version); err != nil {
		t.Fatal(err)
	}
	assertSemanticBatchSearch(t, run, counts, 3)
	run.project.Policy.Embeddings.Model = "changed-model"
	assertSemanticBatchSearch(t, run, counts, 5)
}

func TestSemanticSearchChecksSupportingSourcesBeforeDelivery(t *testing.T) {
	run, _ := batchEmbeddingRun(t, 1)
	record, err := run.Store.Get(run.ctx, "lessons", firstBatchLesson)
	if err != nil {
		t.Fatal(err)
	}
	lesson, err := store.Decode[model.Lesson](record)
	if err != nil {
		t.Fatal(err)
	}
	lesson.Anchors = map[string]string{"missing.go": "old fingerprint"}
	if err := run.Store.Put(run.ctx, "lessons", record.ID, lesson, record.Version); err != nil {
		t.Fatal(err)
	}
	matches, err := run.semanticMatches(run.ctx, batchSemanticQuery)
	if err != nil || len(matches) != 1 {
		t.Fatalf("metadata ranking unnecessarily read source: matches=%d err=%v", len(matches), err)
	}
	_, refs, err := run.lessonBlock(run.ctx, "idempotency", nil)
	if err != nil || len(refs) != 0 {
		t.Fatalf("stale source delivered: refs=%v err=%v", refs, err)
	}
}

func TestSemanticEvaluationRejectsUnavailableLabeledSource(t *testing.T) {
	run, counts := batchEmbeddingRun(t, 1)
	record, err := run.Store.Get(run.ctx, "lessons", firstBatchLesson)
	if err != nil {
		t.Fatal(err)
	}
	lesson, err := store.Decode[model.Lesson](record)
	if err != nil {
		t.Fatal(err)
	}
	lesson.Anchors = map[string]string{"missing.go": "old fingerprint"}
	if err := run.Store.Put(run.ctx, "lessons", record.ID, lesson, record.Version); err != nil {
		t.Fatal(err)
	}
	_, err = run.EvaluateLessonSearch(run.ctx, run.project, []LessonFixture{{Role: "code", Query: batchSemanticQuery, Relevant: []string{record.ID}}})
	if err == nil {
		t.Fatal("evaluation accepted an unavailable labeled source")
	}
	counts.assert(t, 0)
}

func TestAutomaticSemanticSearchKeepsCredentialQueriesLocal(t *testing.T) {
	run, counts := batchEmbeddingRun(t, 1)
	evaluation := LessonEvaluation{Enabled: true, Configuration: semanticConfig(run.project.Policy.Embeddings)}
	if err := run.saveLessonEvaluation(run.ctx, run.project.ID, evaluation); err != nil {
		t.Fatal(err)
	}
	run.learning = &model.LearningSnapshot{}
	lexical, err := run.Store.MatchingLessons(run.ctx, run.project.ID, "code", "idempotency")
	if err != nil {
		t.Fatal(err)
	}
	matches := run.semanticLessons(run.ctx, "idempotency password=private-value", lexical)
	if len(matches) != 1 {
		t.Fatal("sensitive query lost local text matches")
	}
	counts.assert(t, 0)
	cached, err := run.Store.List(run.ctx, "lesson_queries")
	if err != nil || len(cached) != 0 {
		t.Fatalf("sensitive query cached: %d %v", len(cached), err)
	}
	if len(run.learning.Warnings) == 0 {
		t.Fatal("sensitive semantic query bypass had no warning")
	}
}
