package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"khan/internal/github"
	"khan/internal/httpapi"
	"khan/internal/process"
	"khan/internal/provider"
	"khan/internal/store"
	"khan/internal/workflow"
)

// main starts Khan and logs a fatal error if startup or serving fails.
func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run locks the data directory, reaps orphan processes, opens SQLite, and runs the workflow and HTTP listeners until shutdown.
func run() error {
	data := flag.String("data", defaultData(), "private data directory")
	listen := flag.String("listen", "127.0.0.1:8090", "loopback control address")
	webhooks := flag.String("webhook-listen", "", "optional webhook-only listener")
	bridge := flag.String("claude-bridge", "bridges/claude/bridge.mjs", "Claude SDK bridge path")
	flag.Parse()
	absoluteData, absErr := filepath.Abs(*data)
	if absErr != nil {
		return absErr
	}
	*data = absoluteData
	host, _, e := net.SplitHostPort(*listen)
	if e != nil || !(host == "127.0.0.1" || host == "::1" || host == "localhost") {
		return errors.New("control listener must bind loopback")
	}
	if e = os.MkdirAll(*data, 0700); e != nil {
		return e
	}
	lock, e := process.Lock(filepath.Join(*data, "server.lock"))
	if e != nil {
		return fmt.Errorf("another Khan process owns data directory: %w", e)
	}
	defer lock.Close()
	registry := filepath.Join(*data, "processes")
	if e = process.Reap(registry); e != nil {
		return e
	}
	if e = os.Setenv("KHAN_PROCESS_REGISTRY", registry); e != nil {
		return e
	}
	s, e := store.Open(filepath.Join(*data, "khan.db"))
	if e != nil {
		return e
	}
	defer s.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	bridgePath, e := filepath.Abs(*bridge)
	if e != nil {
		return e
	}
	for _, p := range provider.Defaults(bridgePath) {
		if _, e = s.Get(ctx, "providers", p.ID); errors.Is(e, store.ErrNotFound) {
			p.Available = provider.Available(p)
			if p.Kind == "claude" {
				_, e = os.Stat(filepath.Join(filepath.Dir(bridgePath), "node_modules", "@anthropic-ai", "claude-agent-sdk"))
				p.Available = p.Available && e == nil
			}
			if e = s.Put(ctx, "providers", p.ID, p, 0); e != nil {
				return e
			}
		}
	}
	if _, e = s.Get(ctx, "settings", "global"); errors.Is(e, store.ErrNotFound) {
		if e = s.Put(ctx, "settings", "global", map[string]int{"global_slots": 4, "project_slots": 2, "reconciliation_seconds": 300}, 0); e != nil {
			return e
		}
	}
	_ = s.Notify(ctx, "", "startup", "Khan started; interrupted sessions require fresh lease before resumption")
	engine := workflow.New(s, github.CLI{}, *data)
	api := httpapi.New(s, engine, os.Getenv("KHAN_WEBHOOK_SECRET"))
	api.Context = ctx
	server := &http.Server{Addr: *listen, Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	servers := []*http.Server{server}
	if *webhooks != "" {
		if api.Secret == "" {
			return errors.New("KHAN_WEBHOOK_SECRET required for webhook listener")
		}
		servers = append(servers, &http.Server{Addr: *webhooks, Handler: api.WebhookHandler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second})
	}
	errs := make(chan error, len(servers))
	for _, server := range servers {
		go func(server *http.Server) {
			log.Printf("Khan listening on %s", server.Addr)
			errs <- server.ListenAndServe()
		}(server)
	}
	engineDone := make(chan struct{})
	go func() { defer close(engineDone); engine.Run(ctx) }()
	defer func() { cancel(); <-engineDone }()
	select {
	case <-ctx.Done():
	case err := <-errs:
		if !errors.Is(err, http.ErrServerClosed) {
			cancel()
			return err
		}
	}
	shutdown, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	for _, server := range servers {
		_ = server.Shutdown(shutdown)
	}
	return nil
}

// defaultData selects KHAN_DATA_DIR or the user-local Khan state directory, falling back to a relative directory if home lookup fails.
func defaultData() string {
	if v := os.Getenv("KHAN_DATA_DIR"); v != "" {
		return v
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return ".khan-state"
	}
	return filepath.Join(home, ".local", "share", "khan")
}
