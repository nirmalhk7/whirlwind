package provider

import (
	"context"
	"errors"
	"fmt"
	"whirlwind/internal/execution"
	"whirlwind/internal/model"
)

// CheckConnection verifies the configured protocol without sending a model prompt.
// It uses native commands only: status checks never install discovery adapters.
func CheckConnection(ctx context.Context, p model.Provider, cwd string) (string, error) {
	if p.Kind == "codex" {
		return checkCodexAccount(ctx, p, cwd)
	}
	if p.Kind != "acp" && p.Kind != "claude" {
		return "", fmt.Errorf("unsupported provider kind: %s", p.Kind)
	}
	profile, err := execution.ForProvider(cwd, true, "", p.Sandbox, p.Command)
	if err != nil {
		return "", err
	}
	cwd = profile.CWD
	rpc, err := StartWithProfile(ctx, p.Command, profile, nil)
	if err != nil {
		return "", err
	}
	defer rpc.Close()
	if p.Kind == "claude" {
		return checkBridgeCatalog(ctx, rpc, cwd)
	}
	return checkACPSession(ctx, rpc, cwd)
}

func checkCodexAccount(ctx context.Context, p model.Provider, cwd string) (string, error) {
	quota, _, err := (ProcessAdapter{Provider: p}).ReadQuota(ctx, cwd)
	if err != nil {
		return "", err
	}
	if quota == "exhausted" {
		return "", errors.New("provider quota exhausted")
	}
	return "Account connection verified; no inference requested.", nil
}

func checkBridgeCatalog(ctx context.Context, rpc *RPC, cwd string) (string, error) {
	var catalog ModelCatalog
	if err := rpc.Call(ctx, "models", map[string]any{"cwd": cwd}, &catalog); err != nil {
		return "", err
	}
	if len(catalog.Models) == 0 {
		return "", errors.New("provider returned no models")
	}
	return "Bridge and model catalog responding; inference not tested.", nil
}

func checkACPSession(ctx context.Context, rpc *RPC, cwd string) (string, error) {
	s := providerSession{ctx: ctx, input: Input{CWD: cwd, ReadOnly: true}, rpc: rpc}
	if _, err := s.initializeACP(); err != nil {
		return "", err
	}
	var session acpSession
	if err := rpc.Call(ctx, "session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}}, &session); err != nil {
		return "", err
	}
	if session.ID == "" {
		return "", errors.New("ACP returned empty session ID")
	}
	return "Protocol session responding; inference not tested.", nil
}
