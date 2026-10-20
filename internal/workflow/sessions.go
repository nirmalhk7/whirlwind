package workflow

import (
	"context"
	"encoding/json"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

// resumeSession finds the latest stored session matching task, role, provider, worktree, and plan version.
func (e *Engine) resumeSession(ctx context.Context, task, role, providerID, cwd string, version int, models ...string) (string, error) {
	session, err := e.findResumeSession(ctx, task, role, providerID, cwd, version, models...)
	return session.Session, err
}

func (e *Engine) findResumeSession(ctx context.Context, task, role, providerID, cwd string, version int, models ...string) (storedSession, error) {
	expected := sessionRequirementsFor(models)
	rs, err := e.Store.List(ctx, "sessions")
	if err != nil {
		return storedSession{}, err
	}
	var last storedSession
	scope := storedSession{Task: task, Role: role, Provider: providerID, CWD: cwd, Version: version}
	for _, record := range rs {
		candidate, err := e.resumeCandidate(ctx, record, scope, expected)
		if err != nil {
			return last, err
		}
		if candidate.CreatedOrder > last.CreatedOrder {
			last = candidate
		}
	}
	return last, nil
}

func (e *Engine) resumeCandidate(ctx context.Context, record model.Record, scope storedSession, expected sessionRequirements) (storedSession, error) {
	var value storedSession
	if err := json.Unmarshal(record.Data, &value); err != nil {
		return value, err
	}
	if !expected.matchesScope(value, scope) {
		return storedSession{}, nil
	}
	if err := e.resolveSessionTransport(ctx, &value, expected.transport); err != nil {
		return value, err
	}
	if expected.transport != "" && (value.Transport != expected.transport || thinkingKey(value.Thinking) != expected.thinking) {
		return storedSession{}, nil
	}
	value.AttemptID, value.CreatedOrder = record.ID, record.CreatedOrder
	return value, nil
}

func (r sessionRequirements) matchesScope(value, scope storedSession) bool {
	if value.Session == "" || !sessionIdentity(value.Task, value.Role, value.Provider, scope.Task, scope.Role, scope.Provider) {
		return false
	}
	if value.CWD != scope.CWD || value.Version != scope.Version || value.Model != r.model {
		return false
	}
	if r.matchUnit && value.UnitID != r.unit {
		return false
	}
	return !r.matchPurpose || value.Purpose == r.purpose
}

type storedSession struct {
	CreatedOrder int64                 `json:"-"`
	AttemptID    string                `json:"attempt_id"`
	UnitID       string                `json:"unit_id"`
	Task         string                `json:"task"`
	Role         string                `json:"role"`
	Provider     string                `json:"provider"`
	CWD          string                `json:"cwd"`
	Version      int                   `json:"plan_version"`
	Session      string                `json:"session_id"`
	Model        string                `json:"model"`
	Transport    string                `json:"transport"`
	Purpose      string                `json:"purpose"`
	Thinking     *model.ThinkingChoice `json:"thinking"`
}

type sessionRequirements struct {
	model, transport, thinking, purpose, unit string
	matchPurpose, matchUnit                   bool
}

func sessionRequirementsFor(models []string) sessionRequirements {
	expected := sessionRequirements{matchPurpose: len(models) > 3, matchUnit: len(models) > 4}
	if expected.matchUnit {
		expected.unit = models[4]
	}
	if len(models) > 0 {
		expected.model = models[0]
	}
	if len(models) > 3 {
		expected.purpose = models[3]
	}
	if len(models) > 1 {
		expected.transport = models[1]
	}
	if len(models) > 2 {
		expected.thinking = models[2]
	}
	return expected
}

func (e *Engine) resolveSessionTransport(ctx context.Context, session *storedSession, expected string) error {
	if session.Transport == "" && expected != "" {
		p, err := e.Store.Get(ctx, "providers", session.Provider)
		if err != nil {
			return err
		}
		source, err := store.Decode[model.Provider](p)
		if err != nil {
			return err
		}
		session.Transport = source.Kind
	}
	return nil
}

func sessionIdentity(task, role, provider, expectedTask, expectedRole, expectedProvider string) bool {
	return task == expectedTask && role == expectedRole && provider == expectedProvider
}

func thinkingKey(v *model.ThinkingChoice) string {
	if v == nil {
		return ""
	}
	raw, _ := json.Marshal(v)
	return string(raw)
}
