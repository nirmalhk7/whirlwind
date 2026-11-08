package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

type setupFailure struct {
	cause  error
	source model.Provider
	input  provider.Input
}

func (f *setupFailure) Error() string { return f.cause.Error() }
func (f *setupFailure) Unwrap() error { return f.cause }

type setupBlock struct {
	Active       bool           `json:"active"`
	Source       model.Provider `json:"source"`
	Input        provider.Input `json:"input"`
	Fingerprint  string         `json:"fingerprint"`
	Error        string         `json:"error"`
	ChainVersion int            `json:"chain_version"`
	PolicyHash   string         `json:"policy_hash"`
	NextCheckAt  string         `json:"next_check_at,omitempty"`
	CheckedAt    string         `json:"checked_at,omitempty"`
}

func (a *agentRun) wrapSetupFailure(source model.Provider, err error) error {
	if err == nil || (!errors.Is(err, execution.ErrSandbox) && provider.ClassifyFailure(err) != provider.FailureConfiguration) {
		return err
	}
	input := a.input
	input.Prompt = ""
	input.SessionID = ""
	input.Policy = a.project.Policy
	return &setupFailure{cause: err, source: source, input: input}
}

func setupFingerprint(source model.Provider, input provider.Input) string {
	effective := provider.ExecutionProvider(source, input.Thinking)
	profile := input.ExecutionProfile
	if input.Execution != nil {
		profile = input.Execution.Digest()
	}
	raw, _ := json.Marshal(struct {
		Provider, Kind, Model, Profile string
		Command                        []string
		Thinking                       *model.ThinkingChoice
		NativeIntegrations             bool
	}{source.ID, effective.Kind, input.Model, profile, effective.Command, input.Thinking, input.NativeIntegrations})
	return model.Fingerprint(raw)
}

func (e *Engine) saveSetupBlock(ctx context.Context, p model.Project, f *setupFailure) error {
	previous, err := e.Store.Get(ctx, "setup_blocks", p.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	chain, chainErr := e.Store.Get(ctx, "agents", f.input.Role)
	if chainErr != nil && !errors.Is(chainErr, store.ErrNotFound) {
		return chainErr
	}
	block := setupBlock{Active: true, Source: f.source, Input: f.input, Fingerprint: setupFingerprint(f.source, f.input), Error: f.Error(), ChainVersion: chain.Version, PolicyHash: p.PolicyHash}
	return e.Store.Put(ctx, "setup_blocks", p.ID, block, previous.Version)
}

func (e *Engine) checkSetupBlock(ctx context.Context, p model.Project) (bool, bool, error) {
	record, block, err := e.loadSetupBlock(ctx, p)
	if err != nil || !block.Active {
		return err != nil, false, err
	}
	if err = e.refreshSetupConfiguration(ctx, p, &block); err != nil {
		return true, false, err
	}
	if !block.Active {
		return false, true, e.Store.Put(ctx, "setup_blocks", p.ID, block, record.Version)
	}
	next, _ := time.Parse(time.RFC3339Nano, block.NextCheckAt)
	if time.Now().Before(next) {
		return true, false, nil
	}
	return e.probeSetupBlock(ctx, p, record, block)
}

func (e *Engine) loadSetupBlock(ctx context.Context, p model.Project) (model.Record, setupBlock, error) {
	record, err := e.Store.Get(ctx, "setup_blocks", p.ID)
	if errors.Is(err, store.ErrNotFound) {
		if err = e.adoptLegacySetupBlock(ctx, p); err != nil {
			return record, setupBlock{}, err
		}
		record, err = e.Store.Get(ctx, "setup_blocks", p.ID)
	}
	if errors.Is(err, store.ErrNotFound) {
		return record, setupBlock{}, nil
	}
	if err != nil {
		return record, setupBlock{}, err
	}
	b, err := store.Decode[setupBlock](record)
	return record, b, err
}

func (e *Engine) refreshSetupConfiguration(ctx context.Context, p model.Project, b *setupBlock) error {
	source, current, err := e.setupSource(ctx, *b)
	if err != nil || !current {
		b.Active = current
		return err
	}
	source.NativeIntegrations = b.Input.NativeIntegrations
	input := b.Input
	input.Policy = p.Policy
	// A saved inference attempt may have run in a disposable worktree that was
	// removed after its adapter stopped. Rebuild the stable probe configuration
	// from the project authority; execute readiness in a fresh owned snapshot.
	input.CWD = p.Path
	input.Execution = nil
	run := agentRun{Engine: e, ctx: ctx, project: p, task: model.Task{ID: "setup-probe:" + p.ID}, input: input}
	if err = run.prepareExecutionProfile(source); err != nil {
		return err
	}
	// Launcher is intentionally excluded from persisted profiles. Rebuild it and
	// current executable grants before probing; reuse isolated state only when
	// the authority and provider configuration still match the failed attempt.
	if b.Input.Execution != nil && run.input.ExecutionProfile == b.Input.ExecutionProfile && setupFingerprint(source, b.Input) == b.Fingerprint {
		run.input.Execution.RuntimeDir = b.Input.Execution.RuntimeDir
	}
	fingerprint := setupFingerprint(source, run.input)
	if fingerprint != b.Fingerprint {
		b.NextCheckAt = ""
	}
	b.Source = source
	b.Input = run.input
	b.PolicyHash = p.PolicyHash
	b.Fingerprint = fingerprint
	return nil
}

func (e *Engine) setupSource(ctx context.Context, b setupBlock) (model.Provider, bool, error) {
	chain, err := e.Store.Get(ctx, "agents", b.Input.Role)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return model.Provider{}, true, err
	}
	if chain.Version != b.ChainVersion {
		return model.Provider{}, false, nil
	}
	record, err := e.Store.Get(ctx, "providers", b.Source.ID)
	if errors.Is(err, store.ErrNotFound) {
		return model.Provider{}, false, nil
	}
	if err != nil {
		return model.Provider{}, true, err
	}
	source, err := store.Decode[model.Provider](record)
	return source, true, err
}

func (e *Engine) probeSetupBlock(ctx context.Context, p model.Project, record model.Record, b setupBlock) (bool, bool, error) {
	leaseKey := "setup-probe:" + b.Fingerprint
	lease, err := e.Store.Claim(ctx, leaseKey, e.owner, 15*time.Second)
	if errors.Is(err, store.ErrLeased) {
		return true, false, nil
	}
	if err != nil {
		return true, false, err
	}
	defer func() { _ = e.Store.Release(context.Background(), leaseKey, e.owner, lease.Fence) }()
	result, err := e.setupProbeResult(ctx, p, b)
	if err != nil {
		return true, false, err
	}
	b.CheckedAt = result.CheckedAt
	b.NextCheckAt = result.NextCheckAt
	b.Error = result.Error
	b.Active = result.Active
	err = e.Store.Put(ctx, "setup_blocks", p.ID, b, record.Version)
	if err == nil && !b.Active {
		err = e.Store.Notify(ctx, "", "provider_setup", fmt.Sprintf("%s: provider setup recovered; scheduled ideation can continue", p.Name))
	}
	return b.Active || err != nil, !b.Active && err == nil, err
}
