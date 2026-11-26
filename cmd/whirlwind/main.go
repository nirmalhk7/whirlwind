package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"whirlwind/internal/control"
	"whirlwind/internal/execution"
	"whirlwind/internal/github"
	"whirlwind/internal/httpapi"
	"whirlwind/internal/workflow"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run owns the state directory and delegates setup and listener lifecycle to focused helpers.
func run() error {
	config, err := readConfig()
	if err != nil {
		return err
	}
	nonoEnabled, err := execution.NonoEnabled()
	if err != nil {
		return err
	}
	if !nonoEnabled {
		log.Print("WHIRLWIND_NONO=off: native agent and check execution disabled for disposable testing")
	}
	s, lock, err := config.openState()
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	defer func() { _ = s.Close() }()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err = config.initialize(ctx, s); err != nil {
		return err
	}
	engine := workflow.New(s, github.CLI{}, config.data)
	api := httpapi.New(s, engine, os.Getenv("WHIRLWIND_WEBHOOK_SECRET"))
	api.Context = ctx
	return serveSupervisor(ctx, engine, control.SupervisorDispatcher{DB: s.DB, DispatchHealth: engine.DispatchHealth, Commands: localCommandExecutor{api: api, shutdown: cancel}}, control.SocketPath(config.data))
}

func serveSupervisor(ctx context.Context, engine *workflow.Engine, dispatcher control.Dispatcher, socket string) error {
	server, err := control.Listen(socket, dispatcher)
	if err != nil {
		return err
	}
	defer func() { _ = server.Close() }()
	closeQueues, err := control.ListenRoleQueue(ctx, strings.TrimSuffix(socket, ".sock")+"-queues.sock", func(ctx context.Context, queue, action string, request control.RoleQueueRequest) (control.RoleQueueAdmission, error) {
		work := workflow.RoleWork{ProjectID: request.ProjectID, PlanID: request.PlanID, IdeaID: request.IdeaID, Version: request.Version, Queue: queue}
		result, err := engine.RequestRoleQueue(ctx, action, work)
		return control.RoleQueueAdmission{ProjectID: result.ProjectID, PlanID: result.PlanID, Version: result.Version, Status: result.Status}, err
	})
	if err != nil {
		return err
	}
	defer closeQueues()
	engineDone := make(chan struct{})
	go func() { defer close(engineDone); engine.Run(ctx) }()
	<-ctx.Done()
	<-engineDone
	return nil
}
