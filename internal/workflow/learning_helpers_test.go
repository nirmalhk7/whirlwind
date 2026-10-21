package workflow

import (
	"context"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const learningPolicyFixturePath = ".whirlwind.yaml"

func enableLearningIdeation(t *testing.T, e *Engine) {
	t.Helper()
	ctx := context.Background()
	record, _ := e.Store.Get(ctx, "providers", "fake")
	source, _ := store.Decode[model.Provider](record)
	source.Capabilities = append(source.Capabilities, "ideate")
	if err := e.Store.Put(ctx, "providers", "fake", source, record.Version); err != nil {
		t.Fatal(err)
	}
}
