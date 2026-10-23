package provider

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"whirlwind/internal/execution"
)

// ReadQuota reads account metadata through the native command, even when inference uses ACP.
// It never creates a thread or starts a model turn.
func (a ProcessAdapter) ReadQuota(ctx context.Context, cwd string) (string, string, error) {
	if a.Provider.Kind != "codex" {
		return "unknown", "", errors.New("quota metadata requires a Codex provider")
	}
	return a.readCodexQuota(ctx, cwd)
}

func (a ProcessAdapter) readCodexQuota(ctx context.Context, cwd string) (string, string, error) {
	data, err := a.readCodexQuotaData(ctx, cwd)
	if err != nil {
		return "unknown", "", err
	}
	return Quota(data)
}

func (a ProcessAdapter) readCodexQuotaData(ctx context.Context, cwd string) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	profile, err := execution.ForProvider(cwd, true, "", a.Provider.Sandbox, a.Provider.Command)
	if err != nil {
		return nil, err
	}
	rpc, err := StartWithProfile(ctx, a.Provider.Command, profile, nil)
	if err != nil {
		return nil, err
	}
	defer rpc.Close()
	if err = rpc.Call(ctx, "initialize", map[string]any{"clientInfo": map[string]any{"name": "whirlwind", "version": "0.1.0"}}, nil); err != nil {
		return nil, err
	}
	if err = rpc.Notify("initialized", map[string]any{}); err != nil {
		return nil, err
	}
	var data json.RawMessage
	if err = rpc.Call(ctx, "account/rateLimits/read", map[string]any{}, &data); err != nil {
		return nil, err
	}
	return data, nil
}
