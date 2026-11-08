package workflow

import (
	"context"
	"errors"
	"fmt"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func (a *agentRun) executeChoice(source model.Provider, choice model.AgentChoice) (provider.Output, bool, error) {
	a.input.Model, a.input.Thinking = choice.Model, choice.Thinking
	prepared, err := a.prepareChoice(source)
	if err != nil {
		return provider.Output{}, false, err
	}
	return a.executeChoiceAttempts(prepared, choice)
}

func (a *agentRun) prepareChoice(source model.Provider) (model.Provider, error) {
	if a.input.DiscoverConnected {
		a.input.IntegrationScopes = nil
		for _, sourceScope := range a.WorkflowConfiguration(a.ctx).Integrations.Sources {
			if sourceScope.Provider == source.ID {
				a.input.IntegrationScopes = append(a.input.IntegrationScopes, sourceScope)
			}
		}
	}
	for _, target := range a.input.RequiredIntegrations {
		if target.Provider != source.ID {
			return model.Provider{}, fmt.Errorf("integration provider mismatch: %s requires %s; selected %s", target.Ref, target.Provider, source.ID)
		}
	}
	return source, nil
}

func (a *agentRun) executeChoiceAttempts(source model.Provider, choice model.AgentChoice) (provider.Output, bool, error) {
	restarts, retryAt, err := a.persistedRestartState(choice)
	if err != nil {
		return provider.Output{}, false, fmt.Errorf("%w: load recovery budget: %w", store.ErrPersistence, err)
	}
	if restarts > 3 {
		return provider.Output{}, true, fmt.Errorf("%s/%s: restart budget exhausted after 3 restarts", source.ID, choice.Model)
	}
	if retryAt != "" {
		if err = a.waitForPersistedEntryRetry(source, choice, retryAt); err != nil {
			return provider.Output{}, false, err
		}
	}
	for restarts <= 3 {
		a.recoveryRestartCount = restarts
		output, kind, execution, err := a.runChoiceAttempt(source)
		if execution != nil && attemptDeadlineExpired(execution.state, err) {
			return output, true, err
		}
		if err != nil && a.input.Purpose == "integration:create_pr" {
			// A failed transport may already have created the remote PR. Return
			// to the persisted publication stage, which performs lookup first.
			return output, false, err
		}
		if kind == provider.FailureUnknown {
			return output, false, err
		}
		if kind != provider.FailureTransient && kind != provider.FailureRateLimit || restarts == 3 {
			return output, true, fmt.Errorf("%s/%s: %d launch(es), 3 restart(s) maximum: %w", source.ID, choice.Model, restarts+1, err)
		}
		restarts++
		if err := execution.waitRetry(restarts, kind, err); err != nil {
			return output, false, err
		}
	}
	panic("unreachable attempt budget")
}

func (a *agentRun) waitForPersistedRetry(delay time.Duration) error {
	if a.retryWait != nil {
		return a.retryWait(a.ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-a.ctx.Done():
		return a.ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (a *agentRun) quotaAdmissionForChoice(source model.Provider, choice model.AgentChoice) (model.QuotaReserve, error) {
	ownedRetry, err := a.choiceOwnsPersistedCooldown(source, choice)
	if err != nil {
		return model.QuotaReserve{}, err
	}
	if ownedRetry {
		return a.quotaAdmissionWithoutCooldown(source)
	}
	return a.quotaAdmission(source)
}

func (a *agentRun) choiceOwnsPersistedCooldown(source model.Provider, choice model.AgentChoice) (bool, error) {
	entryID := recoveryEntryID(recoveryEpisodeID(a), choice)
	episodeRecord, err := a.Store.Get(a.ctx, "recovery_episodes", entryID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	episode, err := store.Decode[model.RecoveryEpisode](episodeRecord)
	if err != nil {
		return false, err
	}
	if !recoveryChoiceMatchesEpisode(a, choice, entryID, episode) || episode.Status != "failed" || episode.LastAttemptID == "" || episode.NextRetryAt == "" || !retryableEpisodeFailure(episode) {
		return false, nil
	}
	cooldownRecord, err := a.Store.Get(a.ctx, "provider_cooldowns", providerPool(source))
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	cooldown, err := store.Decode[model.ProviderCooldown](cooldownRecord)
	if err != nil {
		return false, err
	}
	return cooldown.AttemptID == episode.LastAttemptID, nil
}

func recoveryChoiceMatchesEpisode(run *agentRun, choice model.AgentChoice, entryID string, episode model.RecoveryEpisode) bool {
	return episode.EntryID == entryID && episode.EpisodeID == recoveryEpisodeID(run) &&
		episode.ProjectID == run.project.ID && episode.TaskID == run.task.ID && episode.PlanVersion == run.task.PlanVersion &&
		episode.Role == run.input.Role && episode.Purpose == run.input.Purpose && episode.UnitID == run.input.UnitID &&
		episode.Provider == choice.Provider && episode.Model == choice.Model &&
		episode.ThinkingKey == thinkingKey(choice.Thinking) && episode.Generation == run.task.RecoveryGeneration
}

func (a *agentRun) waitForPersistedEntryRetry(source model.Provider, choice model.AgentChoice, retryAt string) error {
	deadline, err := parseRetryAt(retryAt)
	if err != nil {
		return err
	}
	episodeID := recoveryEntryID(recoveryEpisodeID(a), choice)
	owner, cooldownDeadline, err := a.persistedRetryCooldown(source, choice, episodeID)
	if err != nil {
		return err
	}
	if cooldownDeadline.After(deadline) {
		deadline = cooldownDeadline
	}
	if delay := time.Until(deadline); delay > 0 {
		if err := a.waitForPersistedRetry(delay); err != nil {
			return err
		}
	}
	if owner == "" {
		return nil
	}
	return a.clearPersistedRetryCooldown(source, choice, episodeID, owner)
}

func (a *agentRun) persistedRetryCooldown(source model.Provider, choice model.AgentChoice, episodeID string) (string, time.Time, error) {
	episodeRecord, err := a.Store.Get(a.ctx, "recovery_episodes", episodeID)
	if errors.Is(err, store.ErrNotFound) {
		return "", time.Time{}, nil
	}
	if err != nil {
		return "", time.Time{}, err
	}
	episode, err := store.Decode[model.RecoveryEpisode](episodeRecord)
	if err != nil {
		return "", time.Time{}, err
	}
	if !recoveryChoiceMatchesEpisode(a, choice, episodeID, episode) || episode.Status != "failed" || episode.LastAttemptID == "" || !retryableEpisodeFailure(episode) {
		return "", time.Time{}, nil
	}
	cooldownRecord, err := a.Store.Get(a.ctx, "provider_cooldowns", providerPool(source))
	if errors.Is(err, store.ErrNotFound) {
		return "", time.Time{}, nil
	}
	if err != nil {
		return "", time.Time{}, err
	}
	cooldown, err := store.Decode[model.ProviderCooldown](cooldownRecord)
	if err != nil {
		return "", time.Time{}, err
	}
	if cooldown.AttemptID != episode.LastAttemptID {
		return "", time.Time{}, nil
	}
	deadline, err := time.Parse(time.RFC3339Nano, cooldown.Until)
	if err != nil {
		return "", time.Time{}, err
	}
	return episode.LastAttemptID, deadline, nil
}

func (a *agentRun) clearPersistedRetryCooldown(source model.Provider, choice model.AgentChoice, episodeID, owner string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(a.ctx), 5*time.Second)
	defer cancel()
	episodeRecord, err := a.Store.Get(ctx, "recovery_episodes", episodeID)
	if err != nil {
		return err
	}
	episode, err := store.Decode[model.RecoveryEpisode](episodeRecord)
	if err != nil {
		return err
	}
	if !recoveryChoiceMatchesEpisode(a, choice, episodeID, episode) || episode.LastAttemptID != owner || episode.Status != "failed" || !retryableEpisodeFailure(episode) {
		return nil
	}
	cooldownRecord, err := a.Store.Get(ctx, "provider_cooldowns", providerPool(source))
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	cooldown, err := store.Decode[model.ProviderCooldown](cooldownRecord)
	if err != nil {
		return err
	}
	if cooldown.AttemptID != owner {
		return nil
	}
	cooldown.Until = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	return a.commitPersistedRetryCooldownClear(ctx, source, choice, episodeID, owner, episodeRecord, episode, cooldownRecord, cooldown)
}

func (a *agentRun) commitPersistedRetryCooldownClear(ctx context.Context, source model.Provider, choice model.AgentChoice, episodeID, owner string, episodeRecord model.Record, episode model.RecoveryEpisode, cooldownRecord model.Record, cooldown model.ProviderCooldown) error {
	if !recoveryChoiceMatchesEpisode(a, choice, episodeID, episode) || episode.LastAttemptID != owner || episode.Status != "failed" || !retryableEpisodeFailure(episode) || cooldown.AttemptID != owner {
		return nil
	}
	cooldown.Until = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	return a.Store.Write(ctx, []store.Change{
		{Kind: "recovery_episodes", ID: episodeID, Value: episode, Expected: episodeRecord.Version},
		{Kind: "provider_cooldowns", ID: providerPool(source), Value: cooldown, Expected: cooldownRecord.Version},
	}, nil)
}

func (a *agentRun) runChoiceAttempt(source model.Provider) (provider.Output, provider.FailureKind, *agentAttempt, error) {
	if err := a.ctx.Err(); err != nil {
		return provider.Output{}, provider.FailureUnknown, nil, err
	}
	if err := a.acquire(a.ctx, a.project); err != nil {
		return provider.Output{}, provider.FailureUnknown, nil, err
	}
	defer a.release(a.project.ID)
	a.input.SessionID = ""
	execution, err := a.newAttempt(source)
	if err != nil {
		err = a.wrapSetupFailure(source, err)
		return provider.Output{}, provider.FailureUnknown, nil, err
	}
	output, err := execution.execute()
	err = a.wrapSetupFailure(source, err)
	kind, failure := execution.failureKind(err)
	if failure != nil {
		return output, provider.FailureUnknown, execution, failure
	}
	return output, kind, execution, err
}

func attemptDeadlineExpired(attempt model.Attempt, err error) bool {
	if !errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	deadline, deadlineErr := attemptDeadline(attempt)
	return deadlineErr == nil && !time.Now().Before(deadline)
}

func (a *agentAttempt) waitRetry(attempt int, kind provider.FailureKind, failure error) error {
	delays := []time.Duration{0, time.Second, 2 * time.Second, 4 * time.Second}
	delay := delays[attempt]
	if reported := provider.RetryAfter(failure); reported > delay {
		delay = reported
	}
	if a.retryWait == nil {
		delay = retryJitter(delay)
	}
	wait, err := a.saveCooldown(delay, "Provider cooldown: "+failure.Error())
	if err != nil {
		return a.persistenceError(err)
	}
	if err := a.persistRetryEvent(map[string]any{"next_attempt": attempt + 1, "delay_ms": delay.Milliseconds(), "reason": failure.Error(), "session_id": a.state.SessionID}); err != nil {
		return a.persistenceError(err)
	}
	if a.retryWait != nil {
		return a.waitForRetryCallback(delay)
	}
	if delay > 30*time.Second {
		return wait
	}
	return a.waitForRetryTimer(delay)
}

func (a *agentAttempt) waitForRetryCallback(delay time.Duration) error {
	if err := a.retryWait(a.ctx, delay); err != nil {
		return err
	}
	return a.clearOwnCooldown()
}

func (a *agentAttempt) waitForRetryTimer(delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-a.ctx.Done():
		return a.ctx.Err()
	case <-timer.C:
		if err := a.ctx.Err(); err != nil {
			return err
		}
		return a.clearOwnCooldown()
	}
}
