package workflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func TestCancellationDuringBackoffStopsChain(t *testing.T) {
	for _, cancelAt := range []int{1, 2} {
		t.Run(string(rune('0'+cancelAt)), func(t *testing.T) {
			h := newRetryHarness(t, model.AgentChoice{Provider: "fake", Model: "first"}, model.AgentChoice{Provider: "fake", Model: "next"})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls, waits := 0, 0
			h.engine.retryWait = func(ctx context.Context, _ time.Duration) error {
				waits++
				if waits == cancelAt {
					cancel()
				}
				return ctx.Err()
			}
			h.engine.Adapter = func(model.Provider) provider.Adapter {
				return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
					calls++
					return provider.Output{}, io.EOF
				})
			}
			_, err := h.run(ctx)
			if !errors.Is(err, context.Canceled) || calls != cancelAt {
				t.Fatalf(retryFailureFormat, calls, err)
			}
		})
	}
}

func TestCancellationDuringExecutionNeverFallsBack(t *testing.T) {
	h := newRetryHarness(t, model.AgentChoice{Provider: "fake", Model: "first"}, model.AgentChoice{Provider: "fake", Model: "next"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	h.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			calls++
			cancel()
			return provider.Output{}, io.EOF
		})
	}
	_, err := h.run(ctx)
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf(retryFailureFormat, calls, err)
	}
	assertOnlyAttemptStatus(t, h.engine, "cancelled")
}

func TestPersistenceFailureDominatesCrash(t *testing.T) {
	h := newRetryHarness(t, model.AgentChoice{Provider: "fake", Model: "m"})
	_, err := h.engine.Store.DB.Exec("CREATE TRIGGER reject_metric BEFORE INSERT ON resources WHEN NEW.kind='metrics' BEGIN SELECT RAISE(ABORT,'metric failure'); END")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	h.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			calls++
			return provider.Output{}, io.EOF
		})
	}
	_, err = h.run(context.Background())
	if !errors.Is(err, store.ErrPersistence) || calls != 1 {
		t.Fatalf(retryFailureFormat, calls, err)
	}
}

func TestFallbackReceivesFreshAttemptAfterPrimaryTimeout(t *testing.T) {
	h := newRetryHarness(t, model.AgentChoice{Provider: "fake", Model: "first"}, model.AgentChoice{Provider: "fake", Model: "next"})
	h.project.Policy.RunTimeoutSeconds = 0
	// Allow real profile/launcher setup under concurrent regression load, while
	// retaining the actual wall-clock timeout and two adapter-start assertions.
	h.engine.attemptTimeout = 10 * time.Second
	calls := 0
	h.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(ctx context.Context, input provider.Input, _ provider.Hooks) (provider.Output, error) {
			calls++
			if input.Model == "first" {
				<-ctx.Done()
				return provider.Output{}, ctx.Err()
			}
			if err := verifyFreshFallbackDeadline(h, ctx); err != nil {
				return provider.Output{}, err
			}
			return provider.Output{Text: "fallback completed"}, nil
		})
	}
	out, err := h.run(context.Background())
	if err != nil || out.Text != "fallback completed" || calls != 2 {
		t.Fatalf("fallback did not outlive primary attempt: calls=%d output=%+v error=%v; %s", calls, out, err, fallbackBudgetDiagnostics(h))
	}
}

func TestExpiredAttemptBeforeAdapterSettlesReservation(t *testing.T) {
	h := newRetryHarness(t, model.AgentChoice{Provider: "fake", Model: "first"})
	h.project.Policy.RunTimeoutSeconds = 0
	h.engine.attemptTimeout = time.Second
	calls := 0
	h.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			calls++
			return provider.Output{Text: "unexpected adapter start"}, nil
		})
	}
	ctx := context.Background()
	run := &agentRun{Engine: h.engine, ctx: ctx, project: h.project, task: h.task, input: provider.Input{Role: "code", Purpose: "primary", Model: "first", CWD: h.cwd, Prompt: "deadline regression"}}
	source := run.configuredProvider("fake")
	attempt := &agentAttempt{agentRun: run, chosen: source, id: store.ID()}
	if _, err := run.reserveAttempt(source, attempt.id); err != nil {
		t.Fatal(err)
	}
	if err := attempt.launch(); err != nil {
		t.Fatal(err)
	}
	record, err := h.engine.Store.Get(ctx, "sessions", attempt.id)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Decode[model.Attempt](record)
	if err != nil {
		t.Fatal(err)
	}
	state.DeadlineAt = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	if err := h.engine.Store.Put(ctx, "sessions", attempt.id, state, record.Version); err != nil {
		t.Fatal(err)
	}
	attempt.state.DeadlineAt = state.DeadlineAt
	_, err = attempt.execute()
	if !errors.Is(err, context.DeadlineExceeded) || !attemptDeadlineExpired(attempt.state, err) || calls != 0 {
		t.Fatalf("expired pre-adapter attempt was not classified as a deadline: calls=%d error=%v", calls, err)
	}
	attempts, err := h.engine.Store.List(context.Background(), "sessions")
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 {
		t.Fatalf("persisted attempts=%d, want one pre-adapter deadline", len(attempts))
	}
	for _, record := range attempts {
		attempt, decodeErr := store.Decode[model.Attempt](record)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if attempt.Status != "failed" || attempt.ProcessState != "deadline_before_adapter" || !strings.Contains(attempt.Error, context.DeadlineExceeded.Error()) {
			t.Fatalf("pre-adapter timeout was not persisted as a failed deadline attempt: %+v", attempt)
		}
	}
	usage, err := h.engine.Store.TaskUsage(context.Background(), h.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if usage.ActiveAttempts != 0 || usage.ReservedRuntimeSeconds != 0 || usage.Attempts != 1 {
		t.Fatalf("pre-adapter timeout left budget reserved or was not accounted: %+v", usage)
	}
}

func fallbackBudgetDiagnostics(h retryHarness) string {
	ctx := context.Background()
	run := &agentRun{Engine: h.engine, ctx: ctx, project: h.project, task: h.task, input: provider.Input{Role: "code"}}
	budget, budgetErr := run.budgetPolicy()
	usage, usageErr := h.engine.Store.TaskUsage(ctx, h.task.ID)
	reservations, reservationErr := h.engine.Store.List(ctx, "budget_reservations")
	var reservationStates []string
	for _, record := range reservations {
		reservation, err := store.Decode[model.BudgetReservation](record)
		if err == nil && reservation.TaskID == h.task.ID {
			reservationStates = append(reservationStates, fmt.Sprintf("%s:%s:%ds", reservation.AttemptID, reservation.Status, reservation.RuntimeSeconds))
		}
	}
	sessions, sessionErr := h.engine.Store.List(ctx, "sessions")
	var sessionStates []string
	for _, record := range sessions {
		attempt, err := store.Decode[model.Attempt](record)
		if err == nil && attempt.Task == h.task.ID {
			sessionStates = append(sessionStates, fmt.Sprintf("%s:%s:%s:%s..%s:%s:%dms", attempt.Model, attempt.Status, attempt.ProcessState, attempt.StartedAt, attempt.DeadlineAt, attempt.Error, valueOrZero(attempt.ElapsedMS)))
		}
	}
	return fmt.Sprintf("run_timeout=%d effective_budget=%+v budget_error=%v usage=%+v usage_error=%v reservations=%v reservation_error=%v attempts=%v session_error=%v", h.project.Policy.RunTimeoutSeconds, budget, budgetErr, usage, usageErr, reservationStates, reservationErr, sessionStates, sessionErr)
}

func valueOrZero(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func TestChildTransportCancellationRetriesWithoutCancellingTask(t *testing.T) {
	h := newRetryHarness(t, model.AgentChoice{Provider: "fake", Model: "m"})
	calls := 0
	h.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			calls++
			if calls == 1 {
				return provider.Output{}, &provider.TransportError{Cause: context.Canceled}
			}
			return provider.Output{Text: "recovered"}, nil
		})
	}
	out, err := h.run(context.Background())
	if err != nil || out.Text != "recovered" || calls != 2 {
		t.Fatalf("child disconnect stopped live task: calls=%d error=%v", calls, err)
	}
}

func assertOnlyAttemptStatus(t *testing.T, engine *Engine, status string) {
	t.Helper()
	records, err := engine.Store.List(context.Background(), "sessions")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("attempt count=%d, want one", len(records))
	}
	attempt, err := store.Decode[model.Attempt](records[0])
	if err != nil {
		t.Fatal(err)
	}
	if attempt.Status != status {
		t.Fatalf("attempt status=%q, want %q", attempt.Status, status)
	}
}

func verifyFreshFallbackDeadline(h retryHarness, ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("fallback context ended before adapter entry: %w", err)
	}
	contextDeadline, hasDeadline := ctx.Deadline()
	if !hasDeadline {
		return errors.New("fallback has no attempt deadline")
	}
	primary, err := persistedRetryAttempt(h, "first")
	if err != nil {
		return err
	}
	fallback, err := persistedRetryAttempt(h, "next")
	if err != nil {
		return err
	}
	primaryDeadline, err := time.Parse(time.RFC3339Nano, primary.DeadlineAt)
	if err != nil {
		return fmt.Errorf("parse primary persisted deadline: %w", err)
	}
	fallbackDeadline, err := time.Parse(time.RFC3339Nano, fallback.DeadlineAt)
	if err != nil {
		return fmt.Errorf("parse fallback persisted deadline: %w", err)
	}
	fallbackStarted, err := time.Parse(time.RFC3339Nano, fallback.StartedAt)
	if err != nil {
		return fmt.Errorf("parse fallback persisted start: %w", err)
	}
	if !fallbackStarted.After(primaryDeadline) || !fallbackDeadline.After(fallbackStarted) {
		return fmt.Errorf("fallback window does not follow expired primary: primary=%s fallback=%s..%s", primaryDeadline, fallbackStarted, fallbackDeadline)
	}
	if !contextDeadline.Equal(fallbackDeadline) {
		return fmt.Errorf("adapter deadline %s differs from persisted fallback deadline %s", contextDeadline, fallbackDeadline)
	}
	if fallbackDeadline.Sub(fallbackStarted) != h.engine.attemptTimeout {
		return fmt.Errorf("fallback allowance=%s, want configured %s", fallbackDeadline.Sub(fallbackStarted), h.engine.attemptTimeout)
	}
	return nil
}

func persistedRetryAttempt(h retryHarness, modelName string) (model.Attempt, error) {
	records, err := h.engine.Store.List(context.Background(), "sessions")
	if err != nil {
		return model.Attempt{}, err
	}
	for _, record := range records {
		attempt, err := store.Decode[model.Attempt](record)
		if err != nil {
			return model.Attempt{}, err
		}
		if attempt.Task == h.task.ID && attempt.Role == "code" && attempt.Model == modelName {
			return attempt, nil
		}
	}
	return model.Attempt{}, fmt.Errorf("no persisted %s attempt", modelName)
}

func TestRetryableEpisodeFailurePrefersRecordedKindWithLegacyFallback(t *testing.T) {
	for _, tc := range []struct {
		name    string
		episode model.RecoveryEpisode
		want    bool
	}{
		{name: "transient kind", episode: model.RecoveryEpisode{FailureKind: string(provider.FailureTransient), Error: "policy denied"}, want: true},
		{name: "rate limit kind", episode: model.RecoveryEpisode{FailureKind: string(provider.FailureRateLimit), Error: "unknown"}, want: true},
		{name: "explicit auth kind", episode: model.RecoveryEpisode{FailureKind: string(provider.FailureAuth), Error: "EOF"}},
		{name: "explicit configuration kind", episode: model.RecoveryEpisode{FailureKind: string(provider.FailureConfiguration), Error: "connection reset"}},
		{name: "explicit policy kind", episode: model.RecoveryEpisode{FailureKind: string(provider.FailurePolicy), Error: "EOF"}},
		{name: "explicit unknown kind", episode: model.RecoveryEpisode{FailureKind: string(provider.FailureUnknown), Error: "EOF"}},
		{name: "legacy transport message", episode: model.RecoveryEpisode{Error: "unexpected EOF"}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := retryableEpisodeFailure(tc.episode); got != tc.want {
				t.Fatalf("retryableEpisodeFailure(%+v)=%v, want %v", tc.episode, got, tc.want)
			}
		})
	}
}

func TestRetryCooldownAdmissionRequiresExactFailedEpisodeOwner(t *testing.T) {
	h := newRetryHarness(t, model.AgentChoice{Provider: "fake", Model: "m"})
	run, source, choice, episode, cooldown := persistedCooldownFixture(t, h, "attempt-one", "attempt-one")
	if err := saveCooldownFixture(t, run, episode, cooldown); err != nil {
		t.Fatal(err)
	}

	owned, err := run.choiceOwnsPersistedCooldown(source, choice)
	if err != nil {
		t.Fatal(err)
	}
	if !owned {
		t.Fatal("exact failed episode should own its cooldown")
	}

	foreign := cooldown
	foreign.AttemptID = "another-live-attempt"
	if err := putCooldown(t, run, source, foreign); err != nil {
		t.Fatal(err)
	}
	owned, err = run.choiceOwnsPersistedCooldown(source, choice)
	if err != nil {
		t.Fatal(err)
	}
	if owned {
		t.Fatal("foreign cooldown was treated as this episode's retry delay")
	}
	if err := run.clearPersistedRetryCooldown(source, choice, episode.EntryID, episode.LastAttemptID); err != nil {
		t.Fatal(err)
	}
	got := loadCooldownFixture(t, run, source)
	if got.AttemptID != foreign.AttemptID || got.Until != foreign.Until {
		t.Fatalf("foreign cooldown changed: got=%+v want=%+v", got, foreign)
	}
}

func TestRetryCooldownClearRechecksEpisodeGenerationAndOwner(t *testing.T) {
	h := newRetryHarness(t, model.AgentChoice{Provider: "fake", Model: "m"})
	run, source, choice, episode, cooldown := persistedCooldownFixture(t, h, "attempt-one", "attempt-one")
	if err := saveCooldownFixture(t, run, episode, cooldown); err != nil {
		t.Fatal(err)
	}

	changed := episode
	changed.LastAttemptID = "attempt-two"
	if err := putEpisodeFixture(t, run, changed); err != nil {
		t.Fatal(err)
	}
	if err := run.clearPersistedRetryCooldown(source, choice, episode.EntryID, episode.LastAttemptID); err != nil {
		t.Fatal(err)
	}
	got := loadCooldownFixture(t, run, source)
	if got.AttemptID != cooldown.AttemptID || got.Until != cooldown.Until {
		t.Fatalf("cooldown for superseded episode changed: got=%+v want=%+v", got, cooldown)
	}

	newEpisode := changed
	newEpisode.Generation++
	if err := putEpisodeFixture(t, run, newEpisode); err != nil {
		t.Fatal(err)
	}
	owned, err := run.choiceOwnsPersistedCooldown(source, choice)
	if err != nil {
		t.Fatal(err)
	}
	if owned {
		t.Fatal("episode with a changed generation retained retry ownership")
	}
}

func TestRetryCooldownClearRejectsStaleCapturedEpisodeVersion(t *testing.T) {
	h := newRetryHarness(t, model.AgentChoice{Provider: "fake", Model: "m"})
	run, source, choice, episode, cooldown := persistedCooldownFixture(t, h, "attempt-one", "attempt-one")
	if err := saveCooldownFixture(t, run, episode, cooldown); err != nil {
		t.Fatal(err)
	}
	episodeRecord, err := run.Store.Get(run.ctx, "recovery_episodes", episode.EntryID)
	if err != nil {
		t.Fatal(err)
	}
	cooldownRecord, err := run.Store.Get(run.ctx, "provider_cooldowns", providerPool(source))
	if err != nil {
		t.Fatal(err)
	}
	capturedEpisode, err := store.Decode[model.RecoveryEpisode](episodeRecord)
	if err != nil {
		t.Fatal(err)
	}
	capturedCooldown, err := store.Decode[model.ProviderCooldown](cooldownRecord)
	if err != nil {
		t.Fatal(err)
	}

	concurrentEpisode := capturedEpisode
	concurrentEpisode.Error = "concurrent retry evidence update"
	if err := run.Store.Put(run.ctx, "recovery_episodes", episode.EntryID, concurrentEpisode, episodeRecord.Version); err != nil {
		t.Fatal(err)
	}
	err = run.commitPersistedRetryCooldownClear(run.ctx, source, choice, episode.EntryID, episode.LastAttemptID, episodeRecord, capturedEpisode, cooldownRecord, capturedCooldown)
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale episode version clear error=%v, want store conflict", err)
	}
	gotCooldown := loadCooldownFixture(t, run, source)
	if gotCooldown.AttemptID != cooldown.AttemptID || gotCooldown.Until != cooldown.Until {
		t.Fatalf("stale episode clear changed cooldown: got=%+v want=%+v", gotCooldown, cooldown)
	}
}

func persistedCooldownFixture(t *testing.T, h retryHarness, attemptID, cooldownOwner string) (*agentRun, model.Provider, model.AgentChoice, model.RecoveryEpisode, model.ProviderCooldown) {
	t.Helper()
	choice := model.AgentChoice{Provider: "fake", Model: "m"}
	run := &agentRun{Engine: h.engine, ctx: context.Background(), project: h.project, task: h.task, input: provider.Input{Role: "code"}}
	source := run.configuredProvider(choice.Provider)
	episodeID := recoveryEpisodeID(run)
	episode := model.RecoveryEpisode{
		EpisodeID:     episodeID,
		EntryID:       recoveryEntryID(episodeID, choice),
		ProjectID:     h.project.ID,
		TaskID:        h.task.ID,
		PlanVersion:   h.task.PlanVersion,
		Role:          run.input.Role,
		Provider:      choice.Provider,
		Model:         choice.Model,
		Generation:    h.task.RecoveryGeneration,
		LastAttemptID: attemptID,
		NextRetryAt:   time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano),
		RestartCount:  1,
		Status:        "failed",
		FailureKind:   string(provider.FailureTransient),
	}
	cooldown := model.ProviderCooldown{
		Pool:      providerPool(source),
		AttemptID: cooldownOwner,
		Until:     time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339Nano),
		Reason:    "retry delay",
	}
	return run, source, choice, episode, cooldown
}

func saveCooldownFixture(t *testing.T, run *agentRun, episode model.RecoveryEpisode, cooldown model.ProviderCooldown) error {
	t.Helper()
	if err := putEpisodeFixture(t, run, episode); err != nil {
		return err
	}
	return putCooldown(t, run, model.Provider{ID: cooldown.Pool}, cooldown)
}

func putEpisodeFixture(t *testing.T, run *agentRun, episode model.RecoveryEpisode) error {
	t.Helper()
	record, err := run.Store.Get(run.ctx, "recovery_episodes", episode.EntryID)
	version := 0
	if err == nil {
		version = record.Version
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	return run.Store.Put(run.ctx, "recovery_episodes", episode.EntryID, episode, version)
}

func putCooldown(t *testing.T, run *agentRun, source model.Provider, cooldown model.ProviderCooldown) error {
	t.Helper()
	pool := providerPool(source)
	record, err := run.Store.Get(run.ctx, "provider_cooldowns", pool)
	version := 0
	if err == nil {
		version = record.Version
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	return run.Store.Put(run.ctx, "provider_cooldowns", pool, cooldown, version)
}

func loadCooldownFixture(t *testing.T, run *agentRun, source model.Provider) model.ProviderCooldown {
	t.Helper()
	record, err := run.Store.Get(run.ctx, "provider_cooldowns", providerPool(source))
	if err != nil {
		t.Fatal(err)
	}
	cooldown, err := store.Decode[model.ProviderCooldown](record)
	if err != nil {
		t.Fatal(err)
	}
	return cooldown
}
