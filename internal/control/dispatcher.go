package control

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"whirlwind/internal/model"
)

type CommandExecutor interface {
	Execute(context.Context, Request) (any, error)
}

type SupervisorDispatcher struct {
	DB             *sql.DB
	Commands       CommandExecutor
	DispatchHealth func() model.DispatchHealth
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
	case DispatchHealthOperation:
		if d.DispatchHealth == nil {
			err = ErrDispatchHealthUnavailable
		} else {
			result = d.DispatchHealth()
		}
	default:
		if !allowedAdapterOperation(request.Operation) {
			return Response{}, fmt.Errorf("unsupported local operation %q", request.Operation)
		}
		if d.Commands == nil {
			return Response{}, errors.New("supervisor command adapter unavailable")
		}
		result, err = d.Commands.Execute(ctx, request)
	}
	return Response{RequestID: request.RequestID, Result: result}, err
}

func allowedAdapterOperation(operation string) bool {
	if !allowedOperations[operation] {
		return false
	}
	switch operation {
	case "inspect.plan", "inspect.artifact", "inspect.memory":
		return true
	default:
		return !strings.HasPrefix(operation, inspectOperationPrefix)
	}
}
