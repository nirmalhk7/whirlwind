// Package memorybridge exposes attempt-scoped memory through a local credentialed bridge.
package memorybridge

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"
)

type Query struct {
	Query string `json:"query" jsonschema:"Current problem or source identifiers to search in project memory"`
}
type Searcher func(context.Context, string) (string, error)
type Endpoint struct {
	URL, Token string
	server     *http.Server
	done       chan struct{}
}

func Start(ctx context.Context, search Searcher) (*Endpoint, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		_ = listener.Close()
		return nil, err
	}
	endpoint := &Endpoint{URL: "http://" + listener.Addr().String(), Token: hex.EncodeToString(secret), done: make(chan struct{})}
	endpoint.server = &http.Server{Handler: endpoint.handler(search), ReadHeaderTimeout: 5 * time.Second}
	go func() { defer close(endpoint.done); _ = endpoint.server.Serve(listener) }()
	go func() {
		select {
		case <-ctx.Done():
			endpoint.Close()
		case <-endpoint.done:
		}
	}()
	return endpoint, nil
}

func (e *Endpoint) Close() { _ = e.server.Close() }

func (e *Endpoint) handler(search Searcher) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+e.Token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != "POST" || r.URL.Path != "/" {
			http.Error(w, "unsupported memory request", http.StatusBadRequest)
			return
		}
		var input Query
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&input); err != nil || strings.TrimSpace(input.Query) == "" || len(input.Query) > 2048 {
			http.Error(w, "invalid memory query", http.StatusBadRequest)
			return
		}
		result, err := search(r.Context(), input.Query)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		if len(result) > 2048 {
			http.Error(w, "memory response too large", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"result": result})
	})
}

var errConnection = errors.New("invalid local memory connection")
