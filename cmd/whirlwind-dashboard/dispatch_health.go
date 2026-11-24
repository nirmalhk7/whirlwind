package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"whirlwind/internal/control"
	"whirlwind/internal/model"
)

type dispatchHealthClient interface {
	Call(context.Context, control.Request) (control.Response, error)
}

func loadDispatchHealth(ctx context.Context, client dispatchHealthClient, snapshot control.Snapshot) (allowProbes bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	response, err := client.Call(ctx, control.Request{RequestID: "dashboard-dispatch-health", Operation: control.DispatchHealthOperation})
	if err != nil {
		unsupported := fmt.Sprintf("unsupported local operation %q", control.DispatchHealthOperation)
		if err.Error() == unsupported || err.Error() == control.ErrDispatchHealthUnavailable.Error() {
			// Older supervisors still support basic inspection and provider probes,
			// but their connectivity does not establish dispatch readiness.
			snapshot[control.DispatchHealthSnapshotKey] = []model.Record{{ID: "local", Data: json.RawMessage(`{"checked":false}`)}}
			return true, nil
		}
		return false, fmt.Errorf("inspect sandbox dispatch health: %w", err)
	}
	data, err := json.Marshal(response.Result)
	if err != nil {
		return false, err
	}
	var health model.DispatchHealth
	if err := json.Unmarshal(data, &health); err != nil {
		return false, fmt.Errorf("decode sandbox dispatch health: %w", err)
	}
	snapshot[control.DispatchHealthSnapshotKey] = []model.Record{{ID: "local", Data: data}}
	return health.Checked && health.BlockReason == "", nil
}
