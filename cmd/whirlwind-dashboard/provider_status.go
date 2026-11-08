package main

import (
	"context"
	"encoding/json"
	"time"
	"whirlwind/internal/control"
	"whirlwind/internal/model"
	"whirlwind/internal/workflow"
)

func loadProviderStatuses(ctx context.Context, client *control.Client, snapshot control.Snapshot) {
	checkCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	response, err := client.Call(checkCtx, control.Request{RequestID: "dashboard-provider-status", Operation: "provider.status"})
	if err != nil {
		return
	}
	data, err := json.Marshal(response.Result)
	if err != nil {
		return
	}
	var statuses workflow.ProviderStatuses
	if json.Unmarshal(data, &statuses) != nil {
		return
	}
	for _, status := range statuses.Providers {
		encoded, err := json.Marshal(status)
		if err == nil {
			snapshot["provider_status"] = append(snapshot["provider_status"], model.Record{ID: status.ID, Data: encoded})
		}
	}
}
