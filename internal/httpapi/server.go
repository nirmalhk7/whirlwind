package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/workflow"
)

type Server struct {
	Context   context.Context
	Store     *store.Store
	Engine    *workflow.Engine
	Secret    string
	mu        sync.Mutex
	webhookMu sync.Mutex
}

// New connects HTTP handlers to storage and the workflow engine with a webhook secret and default background context.
func New(s *store.Store, e *workflow.Engine, secret string) *Server {
	return &Server{Store: s, Engine: e, Secret: secret, Context: context.Background()}
}

// Handler returns the private control API handler with loopback, origin, and mutation protections.
func (s *Server) Handler() http.Handler { return http.HandlerFunc(s.control) }

// Webhoowhirlwinddler exposes only POST /webhooks/github on the separate ingress listener.
func (s *Server) Webhoowhirlwinddler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("POST /webhooks/github", s.webhook)
	return m
}

// private sets no-cache and browser response-protection headers on control responses.
func private(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

// reply writes a JSON response with the requested HTTP status.
func reply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// failure writes the standard error envelope with a fresh request identifier.
func failure(w http.ResponseWriter, status int, code, message string) {
	reply(w, status, map[string]any{"error": map[string]string{"code": code, "message": message, "request_id": store.ID()}})
}

// local accepts only localhost and loopback IP Host values, with an optional valid host/port pair.
func local(host string) bool {
	h := host
	if strings.Contains(host, ":") {
		var e error
		h, _, e = net.SplitHostPort(host)
		if e != nil {
			return false
		}
	}
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// control validates local requests, bounds JSON mutation bodies, serializes mutation handling, and replays saved idempotent responses.

type responseBuffer struct {
	header http.Header
	status int
	body   []byte
}

// Header returns the header map used while buffering a mutation response.
func (b *responseBuffer) Header() http.Header { return b.header }

// WriteHeader records the mutation response status before the response is persisted.
func (b *responseBuffer) WriteHeader(status int) { b.status = status }

// Write appends response bytes for idempotency storage and later delivery.
func (b *responseBuffer) Write(p []byte) (int, error) {
	b.body = append(b.body, p...)
	return len(p), nil
}

// error maps missing resources and version conflicts to HTTP errors, treating other failures as invalid input.
func (s *Server) error(w http.ResponseWriter, e error) {
	var api *apiError
	if errors.As(e, &api) {
		failure(w, api.status, api.code, api.message)
		return
	}
	switch {
	case errors.Is(e, store.ErrNotFound):
		failure(w, 404, "not_found", e.Error())
	case errors.Is(e, store.ErrConflict):
		failure(w, 409, "version_conflict", e.Error())
	default:
		failure(w, 422, "invalid_input", e.Error())
	}
}

// save writes a versioned resource and returns its stored record using the route's success status.
func (s *Server) save(w http.ResponseWriter, r *http.Request, kind, id string, value any, version, status int) {
	if err := s.Store.Put(r.Context(), kind, id, value, version); err != nil {
		s.error(w, err)
		return
	}
	saved, _ := s.Store.Get(r.Context(), kind, id)
	reply(w, status, saved)
}

// expected parses a positive resource version from the If-Match header.
func expected(r *http.Request) (int, error) {
	v := strings.Trim(r.Header.Get("If-Match"), "\"")
	n, e := strconv.Atoi(v)
	if e != nil || n < 1 {
		return 0, errors.New("If-Match positive version required")
	}
	return n, nil
}

// list returns resources of one kind, optionally filtering their project_id by the query parameter.
func (s *Server) list(w http.ResponseWriter, r *http.Request, kind string) {
	records, e := s.Store.List(r.Context(), kind)
	if e != nil {
		s.error(w, e)
		return
	}
	project := r.URL.Query().Get("project")
	if project != "" {
		filtered := []model.Record{}
		for _, record := range records {
			var v struct {
				ProjectID string `json:"project_id"`
			}
			_ = json.Unmarshal(record.Data, &v)
			if v.ProjectID == project {
				filtered = append(filtered, record)
			}
		}
		records = filtered
	}
	reply(w, 200, records)
}

// route dispatches control API routes for onboarding, workflow decisions, resource reads, and version-checked updates.
func (s *Server) route(w http.ResponseWriter, r *http.Request, body []byte) {
	q, err := parseRequest(s, w, r, body)
	if err == nil {
		err = q.dispatch()
	}
	if err != nil {
		s.error(w, err)
	}
}
