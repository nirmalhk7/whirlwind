package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"whirlwind/internal/control"
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
	return serveSupervisor(ctx, engine, control.SupervisorDispatcher{DB: s.DB, Commands: localCommandExecutor{api: api, shutdown: cancel}}, control.SocketPath(config.data))
}

func serveSupervisor(ctx context.Context, engine *workflow.Engine, dispatcher control.Dispatcher, socket string) error {
	server, err := control.Listen(socket, dispatcher)
	if err != nil {
		return err
	}
	defer func() { _ = server.Close() }()
	engineDone := make(chan struct{})
	go func() { defer close(engineDone); engine.Run(ctx) }()
	<-ctx.Done()
	<-engineDone
	return nil
}
