package workflow

import (
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func (a *agentRun) newAttempt(chosen model.Provider) (*agentAttempt, error) {
	if err := a.prepareAttemptResume(chosen); err != nil {
		return nil, err
	}
	execution := &agentAttempt{agentRun: a, chosen: chosen, id: store.ID()}
	execution.state = model.Attempt{AttemptID: execution.id, Task: a.task.ID, UnitID: a.input.UnitID, OperationID: a.input.OperationID, Purpose: a.input.Purpose, PreviousAttemptID: a.previousAttempt, Role: a.input.Role, Provider: chosen.ID, SessionID: a.input.SessionID, RequestedSessionID: a.input.SessionID, CWD: a.input.CWD, PlanVersion: a.task.PlanVersion, Finished: false, Status: "running", Owner: a.owner, ContextCommit: a.index.Commit, ContextIndexVersion: a.index.Version, PolicyHash: a.project.PolicyHash, Model: a.input.Model, ProjectID: a.project.ID, ProviderName: chosen.Name, Transport: provider.ExecutionProvider(chosen, a.input.Thinking).Kind, Thinking: a.input.Thinking, StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := a.Store.Put(a.ctx, "sessions", execution.id, execution.state, 0); err != nil {
		return nil, err
	}
	if err := execution.captureInput(); err != nil {
		return nil, err
	}
	if err := execution.saveStarted(); err != nil {
		return nil, err
	}
	return execution, nil
}

func (a *agentRun) prepareAttemptResume(chosen model.Provider) error {
	if a.input.SessionID == "" && a.task.Recoveries == 0 && !a.freshSession {
		a.input.SessionID = a.resumeSession(a.ctx, a.task.ID, a.input.Role, chosen.ID, a.input.CWD, a.task.PlanVersion, a.input.Model, provider.ExecutionProvider(chosen, a.input.Thinking).Kind, thinkingKey(a.input.Thinking), a.input.Purpose)
	}
	if a.input.SessionID != "" && a.previousAttempt == "" {
		records, err := a.Store.List(a.ctx, "sessions")
		if err != nil {
			return err
		}
		for _, record := range records {
			state, err := store.Decode[model.Attempt](record)
			if err != nil {
				return err
			}
			if a.previousSessionMatches(state, chosen) {
				a.previousAttempt = record.ID
			}
		}
	}
	return nil
}

func (a *agentRun) previousSessionMatches(state model.Attempt, chosen model.Provider) bool {
	sameSession := state.SessionID == a.input.SessionID && state.Task == a.task.ID && state.Role == a.input.Role
	sameConfiguration := state.Provider == chosen.ID && state.CWD == a.input.CWD && state.PlanVersion == a.task.PlanVersion && state.Model == a.input.Model
	return sameSession && sameConfiguration
}

func (a *agentAttempt) captureInput() error {
	prompt, err := a.Store.Capture(a.ctx, a.id, "prompt", "text/plain", []byte(a.input.Prompt), store.MessagePayloadLimit, false)
	if err != nil {
		return err
	}
	snapshot, err := store.SafeJSON(model.AttemptInput{Handoff: a.task.Handoff, Policy: a.project.Policy, Provider: provider.ExecutionProvider(a.chosen, a.input.Thinking), ReadOnly: a.input.ReadOnly, ContextCommit: a.index.Commit, ContextIndexVersion: a.index.Version, Model: a.input.Model, Thinking: a.input.Thinking})
	if err != nil {
		return err
	}
	config, err := a.Store.Capture(a.ctx, a.id, "input_snapshot", "application/json", snapshot, store.MessagePayloadLimit, false)
	if err != nil {
		return err
	}
	a.state.PromptArtifact = prompt.ID
	a.state.InputArtifact = config.ID
	return nil
}

func (a *agentAttempt) saveStarted() error {
	if a.previousAttempt != "" && a.fallbackReason != "" {
		a.event("whirlwind/fallback", map[string]string{"previous_attempt_id": a.previousAttempt, "reason": a.fallbackReason})
	}
	r, err := a.Store.Get(a.ctx, "sessions", a.id)
	if err != nil {
		return err
	}
	if err = a.Store.Put(a.ctx, "sessions", a.id, a.state, r.Version); err != nil {
		return err
	}
	a.previousAttempt = a.id
	return a.eventErr
}
