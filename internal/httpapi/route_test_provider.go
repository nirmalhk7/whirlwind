package httpapi

import (
	"encoding/json"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/workflow"
)

type providerTestOperation struct {
	model.Operation
	Kind           string       `json:"kind"`
	Provider       string       `json:"provider"`
	Stage          string       `json:"stage"`
	Text           string       `json:"text,omitempty"`
	SessionID      string       `json:"session_id,omitempty"`
	Error          string       `json:"error,omitempty"`
	ElapsedMS      int64        `json:"elapsed_ms"`
	Model          string       `json:"model,omitempty"`
	AttemptID      string       `json:"attempt_id,omitempty"`
	ResolvedModel  string       `json:"resolved_model,omitempty"`
	TextTruncated  bool         `json:"text_truncated"`
	OutputArtifact string       `json:"output_artifact,omitempty"`
	Usage          *model.Usage `json:"usage,omitempty"`
}

func (q apiRequest) testProvider() error {
	p, _, err := readCurrent[model.Provider](q, "providers")
	if err != nil {
		return err
	}
	if (p.Kind != "acp" && p.Kind != "codex" && p.Kind != "claude") || len(p.Command) == 0 {
		return &apiError{422, "invalid_input", "Configure a supported agent command before testing connectivity"}
	}
	id := store.ID()
	var input model.AgentChoice
	if err := json.Unmarshal(q.body, &input); err != nil {
		return err
	}
	result := providerTestOperation{Operation: q.operation("provider_test", p.ID, input), Kind: "provider_test", Provider: p.ID, Stage: "running"}
	result.Model = input.Model
	if err := q.Store.Put(q.r.Context(), "operations", id, result, 0); err != nil {
		return err
	}
	store.AfterCommit(q.r.Context(), func() {
		go func() {
			started := time.Now()
			out, err := q.Engine.TestProviderChoice(workflow.WithOperation(q.Context, id), p, input)
			result.Stage = "complete"
			result.Text = string(store.Prefix([]byte(out.Text), 4096))
			result.TextTruncated = len(out.Text) > 4096
			if attempt, getErr := q.Store.Get(q.Context, "sessions", out.AttemptID); getErr == nil {
				state, _ := store.Decode[model.Attempt](attempt)
				result.OutputArtifact = state.OutputArtifact
			}
			result.AttemptID = out.AttemptID
			result.ResolvedModel = out.ResolvedModel
			result.Usage = out.Usage
			result.SessionID = out.SessionID
			result.ElapsedMS = time.Since(started).Milliseconds()
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
