package workflow

import (
	"context"
	"encoding/json"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

// resumeSession finds the latest stored session matching task, role, provider, worktree, and plan version.
func (e *Engine) resumeSession(ctx context.Context, task, role, providerID, cwd string, version int, models ...string) string {
	expected := sessionRequirementsFor(models)
	rs, _ := e.Store.List(ctx, "sessions")
	last := ""
	for _, r := range rs {
		var v storedSession
		_ = json.Unmarshal(r.Data, &v)
		e.resolveSessionTransport(ctx, &v, expected.transport)
		if expected.transport != "" && (v.Transport != expected.transport || thinkingKey(v.Thinking) != expected.thinking) {
			continue
		}
		if expected.matchPurpose && v.Purpose != expected.purpose {
			continue
		}
		if sessionIdentity(v.Task, v.Role, v.Provider, task, role, providerID) && v.CWD == cwd && v.Version == version && v.Model == expected.model {
			last = v.Session
		}
	}
	return last
}

type storedSession struct {
	Task      string                `json:"task"`
	Role      string                `json:"role"`
	Provider  string                `json:"provider"`
	CWD       string                `json:"cwd"`
	Version   int                   `json:"plan_version"`
	Session   string                `json:"session_id"`
	Model     string                `json:"model"`
	Transport string                `json:"transport"`
	Purpose   string                `json:"purpose"`
	Thinking  *model.ThinkingChoice `json:"thinking"`
}

type sessionRequirements struct {
	model, transport, thinking, purpose string
	matchPurpose                        bool
}

func sessionRequirementsFor(models []string) sessionRequirements {
	expected := sessionRequirements{matchPurpose: len(models) > 3}
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

func (e *Engine) resolveSessionTransport(ctx context.Context, session *storedSession, expected string) {
	if session.Transport == "" && expected != "" {
		if p, err := e.Store.Get(ctx, "providers", session.Provider); err == nil {
			source, _ := store.Decode[model.Provider](p)
			session.Transport = source.Kind
		}
	}
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
