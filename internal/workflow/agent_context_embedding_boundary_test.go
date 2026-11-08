package workflow

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	ctxengine "whirlwind/internal/context"
	"whirlwind/internal/model"
)

func TestAgentContextNeverCallsEmbeddingEndpointDirectly(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
	}))
	defer server.Close()
	run := &agentRun{
		ctx:     context.Background(),
		project: model.Project{Policy: model.Policy{Embeddings: &model.Embeddings{Endpoint: server.URL, Model: "test", Enabled: true}}},
		task:    model.Task{Issue: model.Issue{Title: "Improve search", Body: "Current search repeats queries"}},
		index:   ctxengine.Index{EmbeddingValidated: true, Files: map[string]ctxengine.Entry{"search.go": {Path: "search.go", Vector: []float64{1, 0}}}},
	}
	if _, err := run.embeddingEntries(); err == nil {
		t.Fatal("agent context accepted direct embedding inference instead of ACP")
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("agent context sent %d direct embedding requests", got)
	}
}
