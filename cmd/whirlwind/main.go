package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"whirlwind/internal/github"
	"whirlwind/internal/httpapi"
	"whirlwind/internal/memorybridge"
	"whirlwind/internal/workflow"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run dispatches the memory helper before opening control-center state.
func run() error {
	if len(os.Args) > 1 && os.Args[1] == "memory-mcp" {
		return memorybridge.Run(context.Background())
	}
	return runControlCenter()
}

// runControlCenter owns the state directory and listener lifecycle.
func runControlCenter() error {
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
	servers, err := config.servers(api)
	if err != nil {
		return err
	}
	return serve(ctx, cancel, engine, servers)
}
