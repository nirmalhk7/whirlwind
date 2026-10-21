package policy

import (
	"testing"
	"whirlwind/internal/model"
)

const lessonEmbeddingFixtureURL = "https://embeddings.invalid/v1"

func TestLessonEmbeddingsRequireExplicitHTTPEndpointAndModel(t *testing.T) {
	invalid := []model.Embeddings{
		{Model: "configured"},
		{Endpoint: "/embeddings", Model: "configured"},
		{Endpoint: "ftp://embeddings.invalid/v1", Model: "configured"},
		{Endpoint: "https://", Model: "configured"},
		{Endpoint: lessonEmbeddingFixtureURL},
		{Endpoint: lessonEmbeddingFixtureURL, Model: "   "},
	}
	for _, embeddings := range invalid {
		embeddings.LessonsEnabled = true
		p := lessonEmbeddingPolicy(&embeddings)
		if err := Validate(p); err == nil {
			t.Fatalf("invalid lesson embedding config accepted: %+v", embeddings)
		}
	}
}

func TestLessonEmbeddingsAcceptExplicitEndpointsAndRemainOptional(t *testing.T) {
	for _, endpoint := range []string{lessonEmbeddingFixtureURL, "http://127.0.0.1:9000/embed"} {
		config := &model.Embeddings{LessonsEnabled: true, Endpoint: endpoint, Model: "configured"}
		if err := Validate(lessonEmbeddingPolicy(config)); err != nil {
			t.Fatal(err)
		}
	}
	for _, config := range []*model.Embeddings{nil, {LessonsEnabled: false}} {
		if err := Validate(lessonEmbeddingPolicy(config)); err != nil {
			t.Fatal(err)
		}
	}
}

func lessonEmbeddingPolicy(config *model.Embeddings) model.Policy {
	return model.Policy{Checks: []model.Check{{Name: "tests", Command: []string{"go", "test"}}}, AllowedPaths: []string{"src/**"}, RiskRules: []model.RiskRule{{Level: "low", Paths: []string{"src/**"}}}, Embeddings: config}
}
