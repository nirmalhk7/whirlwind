package provider

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
)

type ProcessAdapter struct{ Provider model.Provider }

// Run drives an ACP session over stdio, including streamed output, tool permissions, user questions, and cancellation.
func (a ProcessAdapter) Run(ctx context.Context, in Input, h Hooks) (result Output, err error) {
	p, err := ResolveExecutionProvider(a.Provider)
	if err != nil {
		return Output{SessionID: in.SessionID}, err
	}
	if in.NativeIntegrations {
		p.NativeIntegrations = true
	}
	if !in.ReadinessOnly && !in.CatalogOnly && in.Purpose != "provider_test" {
		if len(in.AgentHooks.Before)+len(in.AgentHooks.After) > 0 {
			return a.runWithAgentHooks(ctx, p, in, h)
		}
	}
	return a.runResolved(ctx, p, in, h)
}

func (a ProcessAdapter) runResolved(ctx context.Context, p model.Provider, in Input, h Hooks) (result Output, err error) {
	in.ProviderID = a.Provider.ID
	in.ACPProfile = p.ACPProfile
	s := &providerSession{ctx: ctx, input: in, hooks: h, client: NewClient(in, h), output: Output{SessionID: in.SessionID}}
	defer func() { err = errors.Join(err, s.client.Close()) }()
	profile, err := s.sessionExecutionProfile(ctx, p, in)
	if err != nil {
		return s.output, err
	}
	standalone := profile.StandaloneRuntime
	transferred := false
	defer func() {
		if standalone && !transferred {
			err = errors.Join(err, completeStandaloneProfile(profile))
		}
	}()
	cleanup, err := s.prepareProviderIntegrations(p, a.Provider, &profile)
	if err != nil {
		return s.output, err
	}
	if cleanup != nil {
		defer cleanup()
	}
	rpc, err := StartWithProfile(ctx, p.Command, profile, s.handle, h.Event)
	if err != nil {
		return s.output, actionableACPError(p, err)
	}
	transferred = true
	s.rpc = rpc
	defer func() { err = errors.Join(err, rpc.Close()) }()
	err = authenticationACPError(p, s.runACP())
	s.mu.Lock()
	result = s.output
	s.mu.Unlock()
	return result, err
}

func (s *providerSession) sessionExecutionProfile(ctx context.Context, executionProvider model.Provider, input Input) (execution.Profile, error) {
	profile, err := s.client.executionProfile()
	if input.Execution != nil {
		profile = *input.Execution
	}
	if err != nil {
		return profile, err
	}
	if input.Execution == nil {
		profile, err = execution.ForProvider(input.CWD, input.ReadOnly, "", input.Policy.Sandbox, executionProvider.Command)
		if err != nil {
			return profile, err
		}
		profile.AllowedPaths = append([]string{}, input.Policy.AllowedPaths...)
		profile, err = pinStandaloneProfile(ctx, profile, "")
		if err != nil {
			return execution.Profile{}, err
		}
	}
	if input.hookRuntimeDir != "" {
		profile.RuntimeDir = input.hookRuntimeDir
	}
	profile.MCPBroker = input.MCPBroker
	profile.MCPBrokerToken = input.MCPBrokerToken
	return profile, nil
}

func (s *providerSession) prepareProviderIntegrations(p, provider model.Provider, profile *execution.Profile) (func(), error) {
	if err := s.prepareSelectedBroker(p, profile); err != nil {
		return nil, err
	}
	if p.Kind == "acp" && p.ACPProfile != "" {
		if err := provisionACPAuth(profile, provider); err != nil {
			return nil, err
		}
	}
	if err := s.prepareNativeIntegrations(p, provider, profile); err != nil {
		return nil, err
	}
	s.input.Execution = profile
	s.input.CWD = profile.CWD
	s.client.input.CWD = profile.CWD
	if p.Kind != "acp" && p.Kind != "claude" {
		return nil, nil
	}
	return s.prepareCheckMCP(s.ctx, profile)
}

func (s *providerSession) prepareSelectedBroker(p model.Provider, profile *execution.Profile) error {
	if s.input.MCPBroker == nil {
		return nil
	}

	if s.input.NativeIntegrations {
		return errors.New("selected read-only MCP broker conflicts with raw native integration mounts")
	}
	if s.hasScopedTool() {
		return errors.New("selected integration broker conflicts with reviewer check broker; use separate agent attempts")
	}
	if p.Kind != "acp" {
		return errors.New("selected integration broker requires the configured ACP transport")
	}
	return s.prepareBrokerMCP(profile)
}

func (s *providerSession) prepareNativeIntegrations(p, provider model.Provider, profile *execution.Profile) error {
	if !s.input.NativeIntegrations {
		return nil
	}
	if err := ProvisionNativeIntegrations(profile, provider); err != nil {
		return err
	}
	if p.Kind == "acp" {
		return s.prepareCursorIntegrations(p, *profile)
	}
	return nil
}

type providerSession struct {
	checkServers   []any
	nativeServers  []any
	nativeCalls    map[string]nativeCall
	nativePolicies map[string]string
	ctx            context.Context
	input          Input
	hooks          Hooks
	client         *Client
	rpc            *RPC
	mu             sync.Mutex
	output         Output
	replaying      atomic.Bool
	messageID      string
}
