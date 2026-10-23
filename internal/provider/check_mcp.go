package provider

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
	"whirlwind/internal/execution"
)

func (s *providerSession) prepareCheckMCP(ctx context.Context, profile *execution.Profile) (func(), error) {
	if !s.hasCheckTool() {
		return func() {}, nil
	}
	root, err := os.MkdirTemp("", "ww-check-")
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	socket := filepath.Join(root, "broker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		os.RemoveAll(root)
		return nil, err
	}
	var random [32]byte
	if _, err = rand.Read(random[:]); err != nil {
		listener.Close()
		os.RemoveAll(root)
		return nil, err
	}
	token := hex.EncodeToString(random[:])
	mux := http.NewServeMux()
	mux.HandleFunc("/run_check", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Whirlwind-Check-Token")), []byte(token)) != 1 {
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 4097))
		if err != nil || len(raw) > 4096 {
			http.Error(w, "invalid request", 400)
			return
		}
		result, err := s.runCheckTool(ctx, raw)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	cleanup := func() { _ = server.Close(); _ = os.RemoveAll(root) }
	node, err := exec.LookPath("node")
	if err != nil {
		cleanup()
		return nil, err
	}
	script := filepath.Join(filepath.Dir(profile.Launcher), "check-tool.mjs")
	if _, err = os.Stat(script); err != nil {
		cleanup()
		return nil, errors.New("review check MCP helper unavailable")
	}
	profile.Sockets = append(profile.Sockets, socket)
	profile.ReadRoots = append(profile.ReadRoots, script)
	s.checkServers = []any{map[string]any{"name": "whirlwind_checks", "command": node, "args": []string{script, socket, token}, "env": []any{}}}
	return cleanup, nil
}
