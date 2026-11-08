package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"whirlwind/internal/control"
	"whirlwind/internal/httpapi"
)

type localCommandExecutor struct {
	api      *httpapi.Server
	shutdown context.CancelFunc
}

func (e localCommandExecutor) Execute(ctx context.Context, request control.Request) (any, error) {
	if request.Operation == "supervisor.stop" {
		if e.shutdown == nil {
			return nil, errors.New("supervisor shutdown unavailable")
		}
		go func() { time.Sleep(100 * time.Millisecond); e.shutdown() }()
		return map[string]string{"status": "stopping"}, nil
	}
	command := httpapi.LocalCommand{
		Operation: request.Operation, ProjectID: request.ProjectID, PlanID: request.PlanID,
		ResourceID: request.ResourceID, ExpectedVersion: request.ExpectedVersion,
		DiffOffset:  request.DiffOffset,
		ContentHash: request.ContentHash, IdempotencyKey: request.IdempotencyKey,
		Payload: request.Payload,
	}
	if e.api == nil {
		return nil, errors.New("local domain handlers unavailable")
	}
	result, err := e.api.ExecuteLocal(ctx, command)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(result), nil
}
