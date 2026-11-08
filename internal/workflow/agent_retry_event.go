package workflow

import (
	"context"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

// persistRetryEvent is the only post-terminal attempt event. It is an audit
// record for a retry already authorized by the exact persisted episode; it
// cannot update session state or publish a stale provider result.
func (a *agentAttempt) persistRetryEvent(data any) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(a.ctx), 5*time.Second)
	defer cancel()
	if err := a.verifyRetryEventOwner(ctx); err != nil {
		return err
	}
	encoded, err := store.SafeJSON(data)
	if err != nil {
		return err
	}
	if _, err := a.Store.Capture(ctx, a.id, "whirlwind/retry", "application/json", encoded, store.MessagePayloadLimit, false); err != nil {
		return err
	}
	return nil
}

func (a *agentAttempt) verifyRetryEventOwner(ctx context.Context) error {
	record, err := a.Store.Get(ctx, "sessions", a.id)
	if err != nil {
		return err
	}
	current, err := store.Decode[model.Attempt](record)
	if err != nil {
		return err
	}
	if !sameTerminalRetryOwner(current, a.state) {
		return store.ErrConflict
	}
	episodeRecord, err := a.Store.Get(ctx, "recovery_episodes", current.RecoveryEpisodeID)
	if err != nil {
		return err
	}
	episode, err := store.Decode[model.RecoveryEpisode](episodeRecord)
	if err != nil {
		return err
	}
	if episode.LastAttemptID != current.AttemptID || episode.Status != "failed" || !retryableEpisodeFailure(episode) {
		return store.ErrConflict
	}
	return nil
}

func sameTerminalRetryOwner(current, expected model.Attempt) bool {
	return current.Finished && current.Status == "failed" &&
		current.AttemptID == expected.AttemptID && current.ExecutionID == expected.ExecutionID &&
		current.ExecutionGeneration == expected.ExecutionGeneration && current.Owner == expected.Owner &&
		current.RecoveryEpisodeID == expected.RecoveryEpisodeID
}
