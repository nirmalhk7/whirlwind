package main

import (
	"context"
	"fmt"
	"path/filepath"

	"whirlwind/internal/control"
	"whirlwind/internal/store"
)

// Preserve the runner stop state when a reachable older supervisor cannot
// expose settings. Read-only access must not recover or write execution state.
func finishOnlineSnapshot(ctx context.Context, data string, snapshot control.Snapshot) (control.Snapshot, error) {
	if _, supported := snapshot["settings"]; !supported {
		reader, err := store.OpenReadOnly(filepath.Join(data, "whirlwind.db"))
		if err != nil {
			return nil, fmt.Errorf("read legacy supervisor settings: %w", err)
		}
		defer func() { _ = reader.Close() }()
		snapshot["settings"], err = reader.List(ctx, "settings")
		if err != nil {
			return nil, fmt.Errorf("read legacy supervisor settings: %w", err)
		}
	}
	return markSupervisor(snapshot, true), nil
}

func legacySettingsUnavailable(kind string, cursor int64, err error) bool {
	return kind == "settings" && cursor == 0 && err.Error() == `resource kind "settings" is not available through local inspection`
}
