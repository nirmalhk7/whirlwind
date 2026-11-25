package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whirlwind/internal/control"
)

func TestRoleQueueHTTPThroughLauncher(t *testing.T) {
	if os.Getenv("WHIRLWIND_TEST_SEEDED_PTY") != "1" {
		t.Skip("set WHIRLWIND_TEST_SEEDED_PTY=1 for the shipped-launcher socket test")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	run := func(action string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, filepath.Join(root, "bin", "whirlwind"), action)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "WHIRLWIND_ROOT="+root, "WHIRLWIND_DATA_DIR="+state,
			"WHIRLWIND_SETTINGS_FILE="+filepath.Join(state, "settings.yaml"))
		if detail, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("launcher %s: %v: %s", action, err, detail)
		}
	}
	run("start")
	t.Cleanup(func() { run("stop") })
	run("status")
	path := strings.TrimSuffix(control.SocketPath(state), ".sock") + "-queues.sock"
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 {
		t.Fatalf("queue listener is missing or not private: %v, %v", info, err)
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 6 * time.Second, Transport: transport}
	for _, tc := range []struct {
		action, body, status string
		code                 int
	}{
		{"push", "{}", "queued", http.StatusOK},
		{"pop", "{}", "waiting", http.StatusOK},
		{"push", `{"plan_id":"missing","version":1}`, "", http.StatusConflict},
		{"ack", "{}", "", http.StatusNotFound},
	} {
		resp, err := client.Post("http://localhost/api/v1/queues/planner_tasks/"+tc.action, "application/json", strings.NewReader(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		var body control.RoleQueueAdmission
		if tc.status != "" {
			err = json.NewDecoder(resp.Body).Decode(&body)
		}
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != tc.code || body.Status != tc.status {
			t.Fatalf("%s got status %d and body %+v: %v", tc.action, resp.StatusCode, body, err)
		}
	}
}
