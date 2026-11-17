package provider

import (
	"context"
	"errors"
	"fmt"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
)

// Run drives a Codex, ACP, or Claude-bridge session over stdio, handling streamed output, tool permissions, user questions, and cancellation.
func (a ProcessAdapter) Run(ctx context.Context, in Input, h Hooks) (result Output, err error) {
	p, err := ResolveExecutionProvider(a.Provider)
	if err != nil {
		return Output{SessionID: in.SessionID}, err
	}
	if in.NativeIntegrations {
		p.NativeIntegrations = true
	}
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
	err = authenticationACPError(p, s.runProviderKind(p.Kind))
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
	profile.MCPBroker = input.MCPBroker
	profile.MCPBrokerToken = input.MCPBrokerToken
	return profile, nil
}

func (s *providerSession) prepareProviderIntegrations(p, provider model.Provider, profile *execution.Profile) (func(), error) {
	if s.input.MCPBroker != nil {
		if s.input.NativeIntegrations {
			return nil, errors.New("selected read-only MCP broker conflicts with raw native integration mounts")
		}
		if s.hasScopedTool() {
			return nil, errors.New("selected integration broker conflicts with reviewer check broker; use separate agent attempts")
		}
		if p.Kind != "acp" {
			return nil, errors.New("selected integration broker requires the configured ACP transport")
		}
		if err := s.prepareBrokerMCP(profile); err != nil {
			return nil, err
		}
	}
	if p.Kind == "acp" && p.ACPProfile != "" {
		if err := provisionACPAuth(profile, provider); err != nil {
			return nil, err
		}
	}
	if s.input.NativeIntegrations {
		if err := ProvisionNativeIntegrations(profile, provider); err != nil {
			return nil, err
		}
		if p.Kind == "acp" {
			if err := s.prepareCursorIntegrations(p, *profile); err != nil {
				return nil, err
			}
		}
	}
	s.input.Execution = profile
	s.input.CWD = profile.CWD
	s.client.input.CWD = profile.CWD
	if p.Kind != "acp" && p.Kind != "claude" {
		return nil, nil
	}
	return s.prepareCheckMCP(s.ctx, profile)
}

func (s *providerSession) runProviderKind(kind string) error {
	switch kind {
	case "acp":
		return s.runACP()
	case "codex":
		return fmt.Errorf("native Codex execution is unsupported; configure its ACP agent and set kind: acp")
	case "claude":
		return fmt.Errorf("Claude bridge execution is unsupported; configure its ACP agent and set kind: acp")
	default:
		return errors.New("unsupported provider kind")
	}
}
