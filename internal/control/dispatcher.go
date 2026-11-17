package control

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type CommandExecutor interface {
	Execute(context.Context, Request) (any, error)
}

type SupervisorDispatcher struct {
	DB       *sql.DB
	Commands CommandExecutor
}

func (d SupervisorDispatcher) Handle(ctx context.Context, request Request) (Response, error) {
	var result any
	var err error
	switch request.Operation {
	case "inspect.snapshot":
		result, err = d.snapshot(ctx, request)
	case "inspect.records":
		result, err = d.records(ctx, request)
	case "inspect.events":
		result, err = d.events(ctx, request)
	default:
		if !allowedOperations[request.Operation] || (strings.HasPrefix(request.Operation, inspectOperationPrefix) && request.Operation != "inspect.plan" && request.Operation != "inspect.artifact") {
			return Response{}, fmt.Errorf("unsupported local operation %q", request.Operation)
		}
		if d.Commands == nil {
			return Response{}, errors.New("supervisor command adapter unavailable")
		}
		result, err = d.Commands.Execute(ctx, request)
	}
	return Response{RequestID: request.RequestID, Result: result}, err
}
