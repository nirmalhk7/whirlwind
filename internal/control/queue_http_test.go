package control

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"whirlwind/internal/store"
	"whirlwind/internal/taskqueue"
	"whirlwind/internal/testutil/queuehttp"
)

func TestNativeQueueHTTPPrivateSocketAndRequestChecks(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	database, err := store.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	path := QueueSocketPath(root)
	client, stop, err := ListenQueues(ctx, path, database)
	if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
		t.Skipf("Unix sockets unavailable in this executor: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	stat, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode()&os.ModeSocket == 0 || stat.Mode().Perm() != 0600 {
		t.Fatalf("not a private Unix socket: %v", stat.Mode())
	}
	if err := client.Push(ctx, taskqueue.Planner, taskqueue.Push{}); err != nil {
		t.Fatal(err)
	}
	if d, err := client.Pop(ctx, taskqueue.Planner); d != nil || err != nil {
		t.Fatalf("empty private endpoint should return 204: %+v, %v", d, err)
	}
	stop()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("queue socket survived shutdown: %v", err)
	}
}

func TestNativeQueueHTTPRequestChecks(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	h := &http.Client{Transport: queuehttp.Transport{Handler: taskqueue.Handler(database)}}
	for _, tc := range []struct {
		path, host, origin, body string
		want                     int
	}{
		{"/api/v1/queues/planner_tasks/pop", "evil.test", "", "{}", 403},
		{"/api/v1/queues/planner_tasks/pop", "localhost", "http://localhost", "{}", 403},
		{"/api/v1/queues/other/pop", "localhost", "", "{}", 404},
		{"/api/v1/queues/planner_tasks/push", "localhost", "", `{"payload":"arbitrary"}`, 400},
		{"/api/v1/queues/planner_tasks/pop", "localhost", "", "null", 400},
		{"/api/v1/queues/planner_tasks/pop", "localhost", "", "{}{}", 400},
		{"/api/v1/queues/planner_tasks/push", "localhost", "", `{"plan_id":"absent","version":1}`, 409},
		{"/webhooks/github", "localhost", "", "{}", 404},
	} {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost"+tc.path, strings.NewReader(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = tc.host
		req.Header.Set("Content-Type", "application/json")
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		res, err := h.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != tc.want {
			t.Errorf("%s %s origin=%q: got %d, want %d", tc.path, tc.host, tc.origin, res.StatusCode, tc.want)
		}
	}
}
