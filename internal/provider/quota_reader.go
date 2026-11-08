package provider

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"whirlwind/internal/execution"
	"whirlwind/internal/model"
)

// ReadQuota reads account metadata through the native command, even when inference uses ACP.
// It never creates a thread or starts a model turn.
func (a ProcessAdapter) ReadQuota(ctx context.Context, cwd string) (string, string, error) {
	supervisorDir, err := defaultStandaloneSupervisorDir()
	if err != nil {
		return "unknown", "", err
	}
	return a.ReadQuotaWithSupervisorDir(ctx, cwd, supervisorDir)
}

// ReadQuotaWithSupervisorDir reads only Codex account metadata while keeping
// the OpenShell runtime locator in the caller's private supervisor registry.
func (a ProcessAdapter) ReadQuotaWithSupervisorDir(ctx context.Context, cwd, supervisorDir string) (string, string, error) {
	if a.Provider.Kind != "codex" {
		return "unknown", "", errors.New("quota metadata requires a Codex provider")
	}
	return a.readCodexQuotaWithSupervisorDir(ctx, cwd, supervisorDir)
}

func (a ProcessAdapter) readCodexQuota(ctx context.Context, cwd string) (string, string, error) {
	supervisorDir, err := defaultStandaloneSupervisorDir()
	if err != nil {
		return "unknown", "", err
	}
	return a.readCodexQuotaWithSupervisorDir(ctx, cwd, supervisorDir)
}

func (a ProcessAdapter) readCodexQuotaWithSupervisorDir(ctx context.Context, cwd, supervisorDir string) (string, string, error) {
	data, err := a.readCodexQuotaDataWithSupervisorDir(ctx, cwd, supervisorDir)
	if err != nil {
		return "unknown", "", err
	}
	return Quota(data)
}

func (a ProcessAdapter) readCodexQuotaData(ctx context.Context, cwd string) (result json.RawMessage, err error) {
	supervisorDir, err := defaultStandaloneSupervisorDir()
	if err != nil {
		return nil, err
	}
	return a.readCodexQuotaDataWithSupervisorDir(ctx, cwd, supervisorDir)
}

func (a ProcessAdapter) readCodexQuotaDataWithSupervisorDir(ctx context.Context, cwd, supervisorDir string) (result json.RawMessage, err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	profile, err := execution.ForProvider(cwd, true, "", a.Provider.Sandbox, a.Provider.Command)
	if err != nil {
		return nil, err
	}
	if err := provisionACPAuth(&profile, model.Provider{ID: a.Provider.ID, Kind: "acp", ACPProfile: "codex"}); err != nil {
		return nil, err
	}
	profile, err = pinStandaloneProfile(ctx, profile, supervisorDir)
	if err != nil {
		return nil, err
	}
	transferred := false
	defer func() {
		if profile.StandaloneRuntime && !transferred {
			err = errors.Join(err, completeStandaloneProfile(profile))
		}
	}()
	rpc, err := StartWithProfile(ctx, a.Provider.Command, profile, nil)
	if err != nil {
		return nil, err
	}
	transferred = profile.StandaloneRuntime
	defer func() { err = errors.Join(err, rpc.Close()) }()
	return readCodexQuotaRPC(ctx, rpc)
}

func readCodexQuotaRPC(ctx context.Context, rpc *RPC) (json.RawMessage, error) {
	if err := rpc.Call(ctx, "initialize", map[string]any{"clientInfo": map[string]any{"name": "whirlwind", "version": "0.1.0"}}, nil); err != nil {
		return nil, err
	}
	if err := rpc.Notify("initialized", map[string]any{}); err != nil {
		return nil, err
	}
	var data json.RawMessage
	if err := rpc.Call(ctx, "account/rateLimits/read", map[string]any{}, &data); err != nil {
		return nil, err
	}
	return data, nil
}
