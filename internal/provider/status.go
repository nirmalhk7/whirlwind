package provider

import (
	"context"
	"errors"
	"whirlwind/internal/execution"
	"whirlwind/internal/model"
)

// CheckConnection verifies the configured ACP protocol without sending a model prompt.
func CheckConnection(ctx context.Context, p model.Provider, cwd string) (result string, err error) {
	return CheckConnectionWithSupervisorDir(ctx, p, cwd, "")
}

// CheckConnectionWithSupervisorDir keeps OpenShell's standalone recovery registry
// alongside the configured supervisor data, without mounting it into the sandbox.
func CheckConnectionWithSupervisorDir(ctx context.Context, p model.Provider, cwd, supervisorDir string) (result string, err error) {
	resolved, err := ResolveExecutionProvider(p)
	if err != nil {
		return "", err
	}
	return checkProtocolConnection(ctx, resolved, cwd, supervisorDir)
}

func checkProtocolConnection(ctx context.Context, p model.Provider, cwd, supervisorDir string) (result string, err error) {
	profile, err := execution.ForProvider(cwd, true, "", p.Sandbox, p.Command)
	if err != nil {
		return "", err
	}
	if p.ACPProfile != "" {
		if err := provisionACPAuth(&profile, p); err != nil {
			return "", err
		}
	}
	profile, err = pinStandaloneProfile(ctx, profile, supervisorDir)
	if err != nil {
		return "", err
	}
	standalone := profile.StandaloneRuntime
	transferred := false
	defer func() {
		if standalone && !transferred {
			err = errors.Join(err, completeStandaloneProfile(profile))
		}
	}()
	cwd = profile.CWD
	rpc, err := StartWithProfile(ctx, p.Command, profile, nil)
	if err != nil {
		return "", actionableACPError(p, err)
	}
	transferred = standalone
	defer func() { err = errors.Join(err, rpc.Close()) }()
	result, err = checkACPSession(ctx, rpc, cwd)
	return result, authenticationACPError(p, err)
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
