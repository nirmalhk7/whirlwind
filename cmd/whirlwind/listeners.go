package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"
	"whirlwind/internal/httpapi"
	"whirlwind/internal/workflow"
)

func (c config) servers(api *httpapi.Server) ([]*http.Server, error) {
	servers := []*http.Server{{Addr: c.listen, Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}}
	if c.webhooks != "" {
		if api.Secret == "" {
			return nil, errors.New("WHIRLWIND_WEBHOOK_SECRET required for webhook listener")
		}
		servers = append(servers, &http.Server{Addr: c.webhooks, Handler: api.Webhoowhirlwinddler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second})
	}
	return servers, nil
}
func serve(ctx context.Context, cancel context.CancelFunc, engine *workflow.Engine, servers []*http.Server) error {
	errs := make(chan error, len(servers))
	for _, server := range servers {
		go func(server *http.Server) {
			log.Printf("Whirlwind listening on %s", server.Addr)
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
