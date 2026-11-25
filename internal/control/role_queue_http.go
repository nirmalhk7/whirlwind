package control

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"whirlwind/internal/store"
)

type RoleQueueRequest struct {
	ProjectID string `json:"project_id,omitempty"`
	PlanID    string `json:"plan_id,omitempty"`
	IdeaID    string `json:"idea_id,omitempty"`
	Version   int    `json:"version,omitempty"`
}

type RoleQueueAdmission struct {
	ProjectID string `json:"project_id,omitempty"`
	PlanID    string `json:"plan_id,omitempty"`
	Version   int    `json:"version,omitempty"`
	Status    string `json:"status"`
}

type RoleQueueHandler func(context.Context, string, string, RoleQueueRequest) (RoleQueueAdmission, error)

// ListenRoleQueue serves admission requests on an owned Unix socket. The
// response never carries authority to renew, release, or complete worker leases.
func ListenRoleQueue(ctx context.Context, path string, execute RoleQueueHandler) (func(), error) {
	listener, absolute, info, err := listenPrivateSocket(path)
	if err != nil {
		return nil, err
	}
	server := &http.Server{Handler: roleQueueHTTP(execute), ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 8192}
	go func() { _ = server.Serve(listener) }()
	return func() {
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if server.Shutdown(shutdown) != nil {
			_ = server.Close()
		}
		_ = listener.Close()
		_ = removeOwnedSocket(absolute, info)
	}, nil
}

func roleQueueHTTP(execute RoleQueueHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Host != "localhost" || r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" {
			http.Error(w, "private local queue endpoint", http.StatusForbidden)
			return
		}
		path, prefix := strings.CutPrefix(r.URL.Path, "/api/v1/queues/")
		queue, action, route := strings.Cut(path, "/")
		if !prefix || !route || queue == "" || (action != "push" && action != "pop") ||
			r.Method != http.MethodPost || r.URL.RawQuery != "" {
			http.NotFound(w, r)
			return
		}
		contentType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if contentType != "application/json" {
			http.Error(w, "application/json required", http.StatusUnsupportedMediaType)
			return
		}
		var request *RoleQueueRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&request) != nil || request == nil || !errors.Is(decoder.Decode(new(any)), io.EOF) {
			http.Error(w, "one valid queue request object required", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()
		result, err := execute(ctx, queue, action, *request)
		if errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrNotFound) {
			http.Error(w, "queue reference or phase is no longer current", http.StatusConflict)
			return
		}
		if err != nil {
			http.Error(w, "queue supervisor unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
}
