package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func recoveryEpisodeID(a *agentRun) string {
	taskID := a.task.ID
	if taskID == "" {
		taskID = a.input.OperationID
	}
	if taskID == "" {
		taskID = a.input.UnitID
	}
	identity := []any{a.project.ID, taskID, a.task.PlanVersion, a.input.Role, a.input.Purpose, a.input.UnitID, a.task.RecoveryGeneration}
	return digest(identity)
}

func recoveryEntryID(episodeID string, choice model.AgentChoice) string {
	return digest([]string{episodeID, choice.Provider, choice.Model, thinkingKey(choice.Thinking)})
}

func digest(value any) string {
	b, _ := json.Marshal(value)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (a *agentRun) persistedRestartState(choice model.AgentChoice) (int, string, error) {
	id := recoveryEntryID(recoveryEpisodeID(a), choice)
	record, err := a.Store.Get(a.ctx, "recovery_episodes", id)
	if err == nil {
		state, decodeErr := store.Decode[model.RecoveryEpisode](record)
		if decodeErr != nil {
			return 0, "", decodeErr
		}
		return nextRestartCount(state), state.NextRetryAt, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return 0, "", err
	}
	count, err := a.legacyRestartCount(choice)
	return count, "", err
}

func nextRestartCount(state model.RecoveryEpisode) int {
	switch state.Status {
	case "interrupted":
		return state.RestartCount + 1
	case "failed":
		if retryableEpisodeFailure(state) {
			return state.RestartCount + 1
		}
	case "complete":
		return 0
	}
	return state.RestartCount
}

func providerRetryableError(message string) bool {
	kind := provider.ClassifyFailure(errors.New(message))
	if kind == provider.FailureTransient || kind == provider.FailureRateLimit {
		return true
	}
	message = strings.ToLower(message)
	for _, marker := range []string{"eof", "connection reset", "connection refused", "broken pipe", "closed pipe", "transport disconnected", "service unavailable", "rate limit", "runtime health lost", "malformed or missing role output"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func retryableEpisodeFailure(episode model.RecoveryEpisode) bool {
	switch episode.FailureKind {
	case string(provider.FailureTransient), string(provider.FailureRateLimit):
		return true
	case "":
		return providerRetryableError(episode.Error)
	default:
		return false
	}
}

func (a *agentRun) legacyRestartCount(choice model.AgentChoice) (int, error) {
	if a.task.RecoveryGeneration > 0 {
		return 0, nil
	}
	records, err := a.Store.List(a.ctx, "sessions")
	if err != nil {
		return 0, err
	}
	count := 0
	for i := len(records) - 1; i >= 0; i-- {
		state, decodeErr := store.Decode[model.Attempt](records[i])
		if decodeErr != nil {
			return 0, decodeErr
		}
		if !legacyAttemptMatches(state, a, choice) {
			continue
		}
		if state.Status == "complete" {
			break
		}
		if state.Status == "interrupted" || state.Status == "failed" && providerRetryableError(state.Error) {
			count++
			continue
		}
		break
	}
	if count > 3 {
		return 4, nil
	}
	return count, nil
}

func legacyAttemptMatches(attempt model.Attempt, run *agentRun, choice model.AgentChoice) bool {
	if attempt.RecoveryEpisodeID != "" {
		return false
	}
	return legacyAttemptIdentityFor(attempt) == legacyRunIdentity(run, choice)
}

type legacyAttemptIdentity struct {
	taskID, projectID string
	planVersion       int
	role, purpose     string
	unitID            string
	provider, model   string
	thinking          string
}

func legacyAttemptIdentityFor(attempt model.Attempt) legacyAttemptIdentity {
	return legacyAttemptIdentity{taskID: attempt.Task, projectID: attempt.ProjectID, planVersion: attempt.PlanVersion, role: attempt.Role, purpose: attempt.Purpose, unitID: attempt.UnitID, provider: attempt.Provider, model: attempt.Model, thinking: thinkingKey(attempt.Thinking)}
}

func legacyRunIdentity(run *agentRun, choice model.AgentChoice) legacyAttemptIdentity {
	return legacyAttemptIdentity{taskID: run.task.ID, projectID: run.project.ID, planVersion: run.task.PlanVersion, role: run.input.Role, purpose: run.input.Purpose, unitID: run.input.UnitID, provider: choice.Provider, model: choice.Model, thinking: thinkingKey(choice.Thinking)}
}

func (a *agentAttempt) recoveryEpisodeChange(ctx context.Context, status, failureKind, retryAt string) (store.Change, error) {
	id := a.state.RecoveryEpisodeID
	record, err := a.Store.Get(ctx, "recovery_episodes", id)
	if err != nil {
		return store.Change{}, err
	}
	episode, err := store.Decode[model.RecoveryEpisode](record)
	if err != nil {
		return store.Change{}, err
	}
	episode.Status = status
	episode.Owner = a.owner
	episode.LastAttemptID = a.id
	episode.RestartCount = a.state.RestartCount
	episode.NextRetryAt = retryAt
	episode.Error = a.state.Error
	episode.FailureKind = failureKind
	episode.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return store.Change{Kind: "recovery_episodes", ID: id, Value: episode, Expected: record.Version}, nil
}

func (a *agentAttempt) startRecoveryEpisode(ctx context.Context, sessionRecord model.Record) error {
	id := a.state.RecoveryEpisodeID
	record, err := a.Store.Get(ctx, "recovery_episodes", id)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	episode := model.RecoveryEpisode{EpisodeID: recoveryEpisodeID(a.agentRun), EntryID: id, ProjectID: a.project.ID, TaskID: a.task.ID, Role: a.input.Role, Purpose: a.input.Purpose, Provider: a.chosen.ID, Model: a.input.Model, ThinkingKey: thinkingKey(a.input.Thinking), UnitID: a.input.UnitID, PlanVersion: a.task.PlanVersion, Generation: a.task.RecoveryGeneration, RestartCount: a.state.RestartCount, Status: "running", Owner: a.owner, LastAttemptID: a.id, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	return a.Store.Write(ctx, []store.Change{{Kind: "sessions", ID: a.id, Value: a.state, Expected: sessionRecord.Version}, {Kind: "recovery_episodes", ID: id, Value: episode, Expected: record.Version}}, nil)
}

func retryDeadline(restarts int) time.Time {
	delays := [...]time.Duration{0, time.Second, 2 * time.Second, 4 * time.Second}
	if restarts < 0 || restarts >= len(delays) {
		return time.Time{}
	}
	return time.Now().Add(delays[restarts])
}

func parseRetryAt(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid recovery retry time: %w", err)
	}
	return parsed, nil
}
