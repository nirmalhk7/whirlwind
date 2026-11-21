package control

import (
	"context"
	"net/http"
	"strings"
	"time"
	"whirlwind/internal/taskqueue"
)

func QueueSocketPath(dataDirectory string) string {
	return strings.TrimSuffix(SocketPath(dataDirectory), ".sock") + "-queues.sock"
}

// ListenQueues starts the task API on a separate private Unix socket. Filesystem
// access, not a localhost Host header, authorizes the local account.
func ListenQueues(ctx context.Context, path string, backend taskqueue.Backend) (*taskqueue.Client, func(), error) {
	listener, _, _, err := listenPrivateSocket(path)
	if err != nil {
		return nil, nil, err
	}
	server := &http.Server{
		Handler: taskqueue.Handler(backend), ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second,
		WriteTimeout: 5 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 8192,
	}
	go func() { _ = server.Serve(listener) }()
	client := taskqueue.NewClient(path)
	return client, func() {
		client.Close()
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
		}
		_ = listener.Close() // also covers shutdown before Serve registered it
	}, nil
}
