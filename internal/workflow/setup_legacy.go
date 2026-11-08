package workflow

import (
	"context"
	"errors"
	"strings"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

// Adopt only retained startup failures; unrelated historical task errors do not block ideation.
func (e *Engine) adoptLegacySetupBlock(ctx context.Context, p model.Project) error {
	schedules, err := e.Store.List(ctx, "schedules")
	if err != nil {
		return err
	}
	var latest model.Record
	for _, r := range schedules {
		if strings.HasPrefix(r.ID, p.ID+":") && r.UpdatedAt > latest.UpdatedAt {
			latest = r
		}
	}
	state, err := store.Decode[struct{ Stage, Error string }](latest)
	if latest.ID == "" {
		return nil
	}
	if err != nil {
		return err
	}
	if state.Stage != "failed" || !legacyStartupError(state.Error) {
		return nil
	}
	return e.adoptLegacyAttempt(ctx, p)
}

func legacyStartupError(message string) bool {
	return strings.Contains(message, "sandbox setup required") || (strings.Contains(message, "before response") && (strings.Contains(message, "exit status 126") || strings.Contains(message, "exit status 127")))
}

func (e *Engine) adoptLegacyAttempt(ctx context.Context, p model.Project) error {
	rows, err := e.Store.List(ctx, "sessions")
	if err != nil {
		return err
	}
	var latest model.Record
	var attempt model.Attempt
	for _, row := range rows {
		candidate, err := store.Decode[model.Attempt](row)
		if err != nil {
			return err
		}
		if candidate.ProjectID == p.ID && candidate.Role == "ideate" && row.UpdatedAt > latest.UpdatedAt {
			latest = row
			attempt = candidate
		}
	}
	if latest.ID == "" || !legacyStartupError(attempt.Error) {
		return nil
	}
	return e.adoptSetupAttempt(ctx, p, attempt)
}

func (e *Engine) adoptSetupAttempt(ctx context.Context, p model.Project, attempt model.Attempt) error {
	record, err := e.Store.Get(ctx, "providers", attempt.Provider)
	if err != nil {
		return err
	}
	source, err := store.Decode[model.Provider](record)
	if err != nil {
		return err
	}
	input := provider.Input{CWD: attempt.CWD, ReadOnly: true, Role: "ideate", Purpose: attempt.Purpose, Model: attempt.Model, Thinking: attempt.Thinking, Policy: p.Policy}
	run := agentRun{Engine: e, ctx: ctx, project: p, task: model.Task{ID: attempt.Task}, input: input}
	if err = run.prepareExecutionProfile(source); err != nil {
		return err
	}
	return e.saveSetupBlock(ctx, p, &setupFailure{cause: errors.New(attempt.Error), source: source, input: run.input})
}
