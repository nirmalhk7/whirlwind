package httpapi

import (
	"encoding/json"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

type providerModelsOperation struct {
	model.Operation
	Kind            string                    `json:"kind"`
	Provider        string                    `json:"provider"`
	Stage           string                    `json:"stage"`
	Models          []provider.ModelInfo      `json:"models"`
	ThinkingOptions []provider.ThinkingOption `json:"thinking_options"`
	Current         string                    `json:"current,omitempty"`
	Error           string                    `json:"error,omitempty"`
}

func (q apiRequest) providerModels() error {
	p, _, err := readCurrent[model.Provider](q, "providers")
	if err != nil {
		return err
	}
	if (p.Kind != "acp" && p.Kind != "codex" && p.Kind != "claude") || len(p.Command) == 0 {
		return &apiError{422, "invalid_input", "Configure a supported provider command first"}
	}
	var input struct {
		Model string `json:"model"`
	}
	if err = json.Unmarshal(q.body, &input); err != nil {
		return err
	}
	id := store.ID()
	result := providerModelsOperation{Operation: q.operation("provider_models", p.ID, input), Kind: "provider_models", Provider: p.ID, Stage: "running", Models: []provider.ModelInfo{}}
	if err := q.Store.Put(q.r.Context(), "operations", id, result, 0); err != nil {
		return err
	}
	store.AfterCommit(q.r.Context(), func() {
		go func() {
			catalog, err := q.Engine.ProviderModels(q.Context, p, input.Model)
			result.Stage = "complete"
			result.Models = catalog.Models
			result.Current = catalog.Current
			result.ThinkingOptions = catalog.ThinkingOptions
			if err != nil {
				result.Stage = "failed"
				result.Error = err.Error()
			}
			result.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
			q.finishOperation(id, result)
		}()
	})
	reply(q.w, 202, map[string]string{"operation_id": id})
	return nil
}
