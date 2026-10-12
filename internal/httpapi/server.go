package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	ctxengine "khan/internal/context"
	"khan/internal/github"
	"khan/internal/model"
	"khan/internal/policy"
	"khan/internal/provider"
	"khan/internal/store"
	"khan/internal/workflow"
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

// WebhookHandler exposes only POST /webhooks/github on the separate ingress listener.
func (s *Server) WebhookHandler() http.Handler {
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
func (s *Server) control(w http.ResponseWriter, r *http.Request) {
	private(w)
	if !local(r.Host) {
		failure(w, 403, "local_host_required", "Loopback Host required")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
		failure(w, 403, "origin_rejected", "Origin must match local host")
		return
	}
	if r.URL.Path == "/api/v1/events" && r.Method == "GET" {
		s.events(w, r)
		return
	}
	if r.Method == "GET" {
		s.route(w, r, nil)
		return
	}
	if r.Method != "POST" && r.Method != "PATCH" {
		failure(w, 404, "route_not_found", "Unsupported route")
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" || len(key) > 256 {
		failure(w, 428, "precondition_required", "Idempotency-Key required")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024*1024))
	if err != nil {
		failure(w, 413, "request_too_large", "Request exceeds 1 MiB")
		return
	}
	if len(body) == 0 {
		body = []byte("{}")
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(body, &object) != nil || object == nil {
		failure(w, 422, "invalid_input", "JSON object required")
		return
	}
	sum := sha256.Sum256([]byte(r.Method + " " + r.URL.RequestURI() + " " + r.Header.Get("If-Match") + " " + string(body)))
	fingerprint := hex.EncodeToString(sum[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	status, cached, err := s.Store.Idempotent(r.Context(), key, fingerprint)
	if err != nil {
		s.error(w, err)
		return
	}
	if status != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(cached)
		return
	}
	buffer := &responseBuffer{header: make(http.Header)}
	s.route(buffer, r, body)
	if buffer.status == 0 {
		buffer.status = 200
	}
	if buffer.status < 500 {
		if err = s.Store.Remember(r.Context(), key, fingerprint, buffer.status, buffer.body); err != nil {
			s.error(w, err)
			return
		}
	}
	for k, v := range buffer.header {
		w.Header()[k] = v
	}
	w.WriteHeader(buffer.status)
	_, _ = w.Write(buffer.body)
}

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
	ctx := r.Context()
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/")
	parts := strings.Split(path, "/")
	kind := parts[0]
	id := ""
	action := ""
	if len(parts) > 1 {
		id = parts[1]
	}
	if len(parts) > 2 {
		action = parts[2]
	}
	if len(parts) > 3 {
		failure(w, 404, "route_not_found", "Unsupported route")
		return
	}
	if kind == "health" && r.Method == "GET" {
		reply(w, 200, map[string]string{"status": "ok"})
		return
	}
	if kind == "overview" && r.Method == "GET" {
		result := map[string]any{}
		for _, k := range []string{"projects", "tasks", "directions", "inbox", "providers"} {
			v, e := s.Store.List(ctx, k)
			if e != nil {
				s.error(w, e)
				return
			}
			result[k] = v
		}
		reply(w, 200, result)
		return
	}
	if kind == "settings" && id == "" {
		if r.Method == "GET" {
			v, e := s.Store.Get(ctx, "settings", "global")
			if errors.Is(e, store.ErrNotFound) {
				reply(w, 200, map[string]any{"id": "global", "version": 0, "data": map[string]any{"global_slots": 4, "project_slots": 2, "reconciliation_seconds": 300}})
				return
			}
			if e != nil {
				s.error(w, e)
				return
			}
			reply(w, 200, v)
			return
		}
		if r.Method == "PATCH" {
			n, e := expected(r)
			if e != nil {
				failure(w, 428, "precondition_required", e.Error())
				return
			}
			var v workflow.Settings
			if e = json.Unmarshal(body, &v); e != nil {
				s.error(w, e)
				return
			}
			if v.GlobalSlots < 1 || v.GlobalSlots > 32 || v.ProjectSlots < 1 || v.ProjectSlots > 32 || v.ReconciliationSeconds < 30 || v.ReconciliationSeconds > 86400 {
				failure(w, 422, "invalid_input", "slots must be 1..32; reconciliation_seconds 30..86400")
				return
			}
			s.save(w, r, "settings", "global", v, n, 200)
			return
		}
	}
	if kind == "projects" && id == "" && r.Method == "POST" {
		var input struct {
			Name       string `json:"name"`
			Path       string `json:"path"`
			WebhookURL string `json:"webhook_url"`
		}
		if e := json.Unmarshal(body, &input); e != nil {
			s.error(w, e)
			return
		}
		if input.Name == "" || !filepath.IsAbs(input.Path) {
			failure(w, 422, "invalid_input", "Name and absolute repository path required")
			return
		}
		canonical, e := filepath.EvalSymlinks(input.Path)
		if e != nil {
			s.error(w, e)
			return
		}
		topLevel, e := github.Run(ctx, canonical, "git", "rev-parse", "--show-toplevel")
		if e != nil {
			s.error(w, e)
			return
		}
		canonical, e = filepath.EvalSymlinks(topLevel)
		if e != nil {
			s.error(w, e)
			return
		}
		p, h, e := policy.Load(canonical)
		if e != nil {
			s.error(w, e)
			return
		}
		repo, e := github.Check(ctx, canonical)
		if e != nil {
			s.error(w, e)
			return
		}
		defaultBranch, e := github.DefaultBranch(ctx, repo)
		if e != nil {
			s.error(w, e)
			return
		}
		sum := sha256.Sum256([]byte(repo))
		project := model.Project{ID: hex.EncodeToString(sum[:8]), Name: input.Name, Repo: repo, DefaultBranch: defaultBranch, Path: canonical, Policy: p, PolicyHash: h, WebhookURL: input.WebhookURL}
		if project.WebhookURL != "" {
			u, e := url.Parse(project.WebhookURL)
			if e != nil || u.Scheme != "https" || u.Host == "" || u.Path != "/webhooks/github" {
				failure(w, 422, "invalid_input", "Webhook URL must be HTTPS ending /webhooks/github")
				return
			}
			if e = s.Engine.GitHub.RegisterWebhook(ctx, repo, project.WebhookURL, s.Secret); e != nil {
				s.error(w, e)
				return
			}
		}
		s.save(w, r, "projects", project.ID, project, 0, 201)
		return
	}
	if kind == "providers" && id == "" && r.Method == "POST" {
		var p model.Provider
		if e := json.Unmarshal(body, &p); e != nil {
			s.error(w, e)
			return
		}
		if p.ID == "" || p.Name == "" || len(p.Command) == 0 || p.Kind != "acp" {
			failure(w, 422, "invalid_input", "Custom provider needs id, name, kind=acp and command array")
			return
		}
		p.Available = provider.Available(p)
		p.Quota = "unknown"
		s.save(w, r, "providers", p.ID, p, 0, 201)
		return
	}
	if kind == "projects" && action == "ideation" && r.Method == "POST" {
		p, e := s.Engine.Project(ctx, id)
		if e != nil {
			s.error(w, e)
			return
		}
		operation := store.ID()
		_ = s.Store.Put(ctx, "operations", operation, map[string]string{"stage": "running"}, 0)
		go func() {
			err := s.Engine.Ideate(s.Context, p, workflow.DueDay(time.Now()))
			v := map[string]string{"stage": "complete"}
			if err != nil {
				v["stage"] = "failed"
				v["error"] = err.Error()
				_ = s.Store.Notify(context.Background(), "", "ideation", err.Error())
			}
			record, _ := s.Store.Get(context.Background(), "operations", operation)
			_ = s.Store.Put(context.Background(), "operations", operation, v, record.Version)
		}()
		reply(w, 202, map[string]string{"operation_id": operation})
		return
	}
	if kind == "projects" && action == "policy" && r.Method == "POST" {
		n, e := expected(r)
		if e != nil {
			failure(w, 428, "precondition_required", e.Error())
			return
		}
		p, e := s.Engine.Project(ctx, id)
		if e != nil {
			s.error(w, e)
			return
		}
		updated, h, e := policy.Load(p.Path)
		if e != nil {
			s.error(w, e)
			return
		}
		p.Policy = updated
		p.PolicyHash = h
		s.save(w, r, "projects", id, p, n, 200)
		return
	}
	if kind == "projects" && action == "embedding-evaluation" && r.Method == "POST" {
		p, e := s.Engine.Project(ctx, id)
		if e != nil {
			s.error(w, e)
			return
		}
		if p.Policy.Embeddings == nil {
			failure(w, 422, "invalid_input", "Configure embedding provider in committed policy")
			return
		}
		var input struct {
			Fixtures []ctxengine.Fixture `json:"fixtures"`
		}
		if e = json.Unmarshal(body, &input); e != nil {
			s.error(w, e)
			return
		}
		record, e := s.Store.Get(ctx, "indexes", id)
		if e != nil {
			s.error(w, e)
			return
		}
		idx, e := store.Decode[ctxengine.Index](record)
		if e != nil {
			s.error(w, e)
			return
		}
		result, e := idx.Evaluate(ctx, *p.Policy.Embeddings, input.Fixtures)
		if e != nil {
			s.error(w, e)
			return
		}
		if e = s.Store.Put(ctx, "indexes", id, idx, record.Version); e != nil {
			s.error(w, e)
			return
		}
		reply(w, 200, result)
		return
	}
	if kind == "directions" && action == "select" && r.Method == "POST" {
		n, e := expected(r)
		if e != nil {
			failure(w, 428, "precondition_required", e.Error())
			return
		}
		record, e := s.Store.Get(ctx, "directions", id)
		if e != nil {
			s.error(w, e)
			return
		}
		if record.Version != n {
			s.error(w, store.ErrConflict)
			return
		}
		d, e := store.Decode[model.Direction](record)
		if e != nil {
			s.error(w, e)
			return
		}
		taskID := "direction-" + d.ID
		task := model.Task{ID: taskID, ProjectID: d.ProjectID, Issue: model.Issue{Title: d.Title, Body: d.Description}, PlanVersion: 1, Stage: "planning", Handoff: model.Handoff{Version: 1, NextAction: "plan selected direction"}}
		if e = s.Store.Put(ctx, "tasks", taskID, task, 0); e != nil && !errors.Is(e, store.ErrConflict) {
			s.error(w, e)
			return
		}
		d.Selected = true
		if e = s.Store.Put(ctx, "directions", id, d, n); e != nil {
			s.error(w, e)
			return
		}
		saved, _ := s.Store.Get(ctx, "tasks", taskID)
		reply(w, 202, saved)
		return
	}
	if kind == "tasks" && action != "" && r.Method == "POST" {
		n, e := expected(r)
		if e != nil {
			failure(w, 428, "precondition_required", e.Error())
			return
		}
		record, e := s.Store.Get(ctx, "tasks", id)
		if e != nil {
			s.error(w, e)
			return
		}
		if record.Version != n {
			s.error(w, store.ErrConflict)
			return
		}
		t, e := store.Decode[model.Task](record)
		if e != nil {
			s.error(w, e)
			return
		}
		var input struct {
			Answers map[string]string `json:"answers"`
		}
		switch action {
		case "pause":
			if t.Stage == "complete" || t.Stage == "pr_created" {
				failure(w, 409, "invalid_stage", "Published work has no running task to pause")
				return
			}
			if t.Stage != "paused" {
				t.ResumeStage = t.Stage
			}
			s.Engine.Stop(id)
			t.Stage = "paused"
			t.Error = "Paused by user"
		case "resume":
			if t.Stage != "paused" {
				failure(w, 409, "invalid_stage", "Only paused tasks resume")
				return
			}
			t.Error = ""
			t.Stage = "planning"
			if t.Plan != nil && t.Worktree != "" {
				t.Stage = "repair"
				for _, unit := range t.Plan.Units {
					if unit.Stage != "integrated" {
						t.Stage = "coding"
					}
				}
			} else if t.Plan != nil {
				t.Stage = "coding"
			}
			if t.PR != "" {
				failure(w, 409, "invalid_stage", "Review existing published PR in GitHub")
				return
			}
			if t.ResumeStage != "" && t.ResumeStage != "paused" {
				t.Stage = t.ResumeStage
			} else if t.Plan != nil {
				for _, question := range t.Plan.Questions {
					if question.Answer == "" {
						t.Stage = "questions"
					}
				}
			}
			t.EligibilityOverride = !t.Issue.Eligible()
			t.UnchangedFailures = 0
			t.Recoveries = 0
		case "answers":
			if t.Stage != "questions" {
				failure(w, 409, "invalid_stage", "Task has no pending questions")
				return
			}
			if e = json.Unmarshal(body, &input); e != nil {
				s.error(w, e)
				return
			}
			for n := range t.Plan.Questions {
				q := &t.Plan.Questions[n]
				if a, ok := input.Answers[q.ID]; ok {
					q.Answer = strings.TrimSpace(a)
				}
			}
			complete := true
			for _, q := range t.Plan.Questions {
				if q.Answer == "" {
					complete = false
				}
			}
			if complete {
				t.Stage = "planning"
			}
		case "approve":
			if t.Stage != "approval" {
				failure(w, 409, "invalid_stage", "Task does not await critical approval")
				return
			}
			t.Approved = true
			t.Stage = "publishing"
		default:
			failure(w, 404, "route_not_found", "Unknown task action")
			return
		}
		s.save(w, r, "tasks", id, t, n, 200)
		return
	}
	if kind == "questions" && action == "answers" && r.Method == "POST" {
		n, x := expected(r)
		if x != nil {
			failure(w, 428, "precondition_required", x.Error())
			return
		}
		record, x := s.Store.Get(ctx, "questions", id)
		if x != nil {
			s.error(w, x)
			return
		}
		set, x := store.Decode[model.QuestionSet](record)
		if x != nil {
			s.error(w, x)
			return
		}
		if set.Status != "pending" {
			failure(w, 409, "invalid_stage", "Question is no longer pending")
			return
		}
		var input struct {
			Answers map[string]string `json:"answers"`
		}
		if x = json.Unmarshal(body, &input); x != nil {
			s.error(w, x)
			return
		}
		for _, q := range set.Questions {
			if strings.TrimSpace(input.Answers[q.ID]) == "" {
				failure(w, 422, "invalid_input", "Answer every question")
				return
			}
		}
		set.Answers = input.Answers
		set.Status = "answered"
		s.save(w, r, "questions", id, set, n, 200)
		return
	}
	if kind == "permissions" && action == "decisions" && r.Method == "POST" {
		n, e := expected(r)
		if e != nil {
			failure(w, 428, "precondition_required", e.Error())
			return
		}
		record, e := s.Store.Get(ctx, "permissions", id)
		if e != nil {
			s.error(w, e)
			return
		}
		var p map[string]any
		_ = json.Unmarshal(record.Data, &p)
		var input struct {
			Decision string `json:"decision"`
		}
		_ = json.Unmarshal(body, &input)
		if input.Decision != "allow" && input.Decision != "deny" {
			failure(w, 422, "invalid_input", "decision must be allow or deny")
			return
		}
		if p["decision"] != "pending" {
			failure(w, 409, "already_decided", "Permission already answered")
			return
		}
		p["decision"] = input.Decision
		s.save(w, r, "permissions", id, p, n, 200)
		return
	}
	kinds := map[string]bool{"projects": true, "providers": true, "directions": true, "tasks": true, "plans": true, "reviews": true, "findings": true, "permissions": true, "questions": true, "inbox": true, "metrics": true, "outcomes": true, "sessions": true, "checkpoints": true, "units": true, "indexes": true, "index_history": true, "verification": true, "operations": true}
	if !kinds[kind] || action != "" {
		failure(w, 404, "route_not_found", "Unsupported route")
		return
	}
	if r.Method == "GET" {
		if id == "" {
			s.list(w, r, kind)
			return
		}
		record, e := s.Store.Get(ctx, kind, id)
		if e != nil {
			s.error(w, e)
			return
		}
		w.Header().Set("ETag", fmt.Sprintf("\"%d\"", record.Version))
		reply(w, 200, record)
		return
	}
	if r.Method == "PATCH" && (kind == "providers" || kind == "inbox") {
		n, e := expected(r)
		if e != nil {
			failure(w, 428, "precondition_required", e.Error())
			return
		}
		record, e := s.Store.Get(ctx, kind, id)
		if e != nil {
			s.error(w, e)
			return
		}
		if record.Version != n {
			s.error(w, store.ErrConflict)
			return
		}
		var value any
		if kind == "providers" {
			var p model.Provider
			if e = json.Unmarshal(body, &p); e != nil {
				s.error(w, e)
				return
			}
			old, _ := store.Decode[model.Provider](record)
			p.ID = id
			if p.Kind != old.Kind || p.Kind == "unavailable" || len(p.Command) == 0 {
				failure(w, 422, "invalid_input", "Provider kind cannot change; command required")
				return
			}
			p.Available = provider.Available(p)
			p.Quota = "unknown"
			value = p
		} else {
			old, _ := store.Decode[model.Notification](record)
			old.Resolved = true
			value = old
		}
		s.save(w, r, kind, id, value, n, 200)
		return
	}
	failure(w, 404, "route_not_found", "Unsupported route")
}

// events replays ordered SQLite events after the requested cursor and streams new events or idle heartbeats until disconnection.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("after")
	if raw == "" {
		raw = r.Header.Get("Last-Event-ID")
	}
	if raw == "" {
		raw = "0"
	}
	cursor, e := strconv.ParseInt(raw, 10, 64)
	if e != nil || cursor < 0 || cursor > 9007199254740991 {
		failure(w, 422, "invalid_cursor", "Non-negative safe integer cursor required")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		failure(w, 500, "stream_unavailable", "Streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		events, e := s.Store.Events(r.Context(), cursor, 100)
		if e != nil {
			return
		}
		for _, event := range events {
			b, _ := json.Marshal(event)
			if _, e = fmt.Fprintf(w, "id: %d\nevent: update\ndata: %s\n\n", event.ID, b); e != nil {
				return
			}
			cursor = event.ID
		}
		if len(events) == 0 {
			if _, e = io.WriteString(w, ": heartbeat\n\n"); e != nil {
				return
			}
		}
		flusher.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

// webhook validates signed issue deliveries, reconciles their current state, ingests registered repositories, and records successful receipt.
func (s *Server) webhook(w http.ResponseWriter, r *http.Request) {
	s.webhookMu.Lock()
	defer s.webhookMu.Unlock()
	body, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024*1024))
	if e != nil {
		failure(w, 413, "request_too_large", "Webhook too large")
		return
	}
	if !github.ValidSignature(s.Secret, r.Header.Get("X-Hub-Signature-256"), body) {
		failure(w, 403, "invalid_signature", "Webhook signature invalid")
		return
	}
	delivery := r.Header.Get("X-GitHub-Delivery")
	if delivery == "" {
		failure(w, 422, "invalid_input", "Delivery ID required")
		return
	}
	var received string
	if err := s.Store.DB.QueryRowContext(r.Context(), "SELECT received_at FROM deliveries WHERE id=?", delivery).Scan(&received); err == nil {
		reply(w, 202, map[string]bool{"accepted": true})
		return
	}
	if r.Header.Get("X-GitHub-Event") == "ping" {
		reply(w, 202, map[string]bool{"accepted": true})
		return
	}
	if r.Header.Get("X-GitHub-Event") != "issues" {
		reply(w, 202, map[string]bool{"ignored": true})
		return
	}
	var v struct {
		Repository struct {
			Name string `json:"full_name"`
		} `json:"repository"`
		Issue struct {
			Number    int    `json:"number"`
			Title     string `json:"title"`
			Body      string `json:"body"`
			State     string `json:"state"`
			UpdatedAt string `json:"updated_at"`
			URL       string `json:"html_url"`
			Labels    []struct {
				Name string `json:"name"`
			} `json:"labels"`
		} `json:"issue"`
	}
	if json.Unmarshal(body, &v) != nil || v.Issue.Number <= 0 {
		failure(w, 422, "invalid_input", "Invalid issue webhook")
		return
	}
	projects, e := s.Store.List(r.Context(), "projects")
	if e != nil {
		failure(w, 503, "unavailable", e.Error())
		return
	}
	for _, record := range projects {
		p, _ := store.Decode[model.Project](record)
		if p.Repo != v.Repository.Name {
			continue
		}
		i := model.Issue{Number: v.Issue.Number, Title: v.Issue.Title, Body: v.Issue.Body, State: v.Issue.State, UpdatedAt: v.Issue.UpdatedAt, URL: v.Issue.URL}
		for _, l := range v.Issue.Labels {
			i.Labels = append(i.Labels, l.Name)
		}
		if p.DefaultBranch != "" {
			current, x := s.Engine.GitHub.Issue(r.Context(), p.Repo, i.Number)
			if x != nil {
				failure(w, 503, "github_unavailable", x.Error())
				return
			}
			i = current
		}
		if _, e = s.Engine.Ingest(r.Context(), p, i); e != nil && !errors.Is(e, store.ErrNotFound) {
			failure(w, 503, "unavailable", e.Error())
			return
		}
		if _, e = s.Store.Delivery(r.Context(), delivery); e != nil {
			failure(w, 503, "unavailable", e.Error())
			return
		}
		reply(w, 202, map[string]bool{"accepted": true})
		return
	}
	failure(w, 404, "project_not_found", "Repository not onboarded")
}
