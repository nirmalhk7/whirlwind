package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

type webhookPayload struct {
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

func (v webhookPayload) issue() model.Issue {
	i := model.Issue{Number: v.Issue.Number, Title: v.Issue.Title, Body: v.Issue.Body, State: v.Issue.State, UpdatedAt: v.Issue.UpdatedAt, URL: v.Issue.URL}
	for _, label := range v.Issue.Labels {
		i.Labels = append(i.Labels, label.Name)
	}
	return i
}

// webhook serializes signed deliveries and records successful reconciliation.
func (s *Server) webhook(w http.ResponseWriter, r *http.Request) {
	s.webhookMu.Lock()
	defer s.webhookMu.Unlock()
	result, err := s.receiveWebhook(w, r)
	if err != nil {
		s.error(w, err)
	} else {
		reply(w, 202, result)
	}
}
func (s *Server) readSignedWebhook(w http.ResponseWriter, r *http.Request) ([]byte, string, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024*1024))
	if err != nil {
		return nil, "", &apiError{413, "request_too_large", "Webhook too large"}
	}
	if !github.ValidSignature(s.Secret, r.Header.Get("X-Hub-Signature-256"), body) {
		return nil, "", &apiError{403, "invalid_signature", "Webhook signature invalid"}
	}
	delivery := r.Header.Get("X-GitHub-Delivery")
	if delivery == "" {
		return nil, "", &apiError{422, "invalid_input", "Delivery ID required"}
	}
	return body, delivery, nil
}
func (s *Server) receiveWebhook(w http.ResponseWriter, r *http.Request) (map[string]bool, error) {
	if s.Engine.Agentic(r.Context()) {
		return map[string]bool{"ignored": true}, nil
	}
	body, delivery, err := s.readSignedWebhook(w, r)
	if err != nil {
		return nil, err
	}
	var received string
	err = s.Store.DB.QueryRowContext(r.Context(), "SELECT received_at FROM deliveries WHERE id=?", delivery).Scan(&received)
	if err == nil || r.Header.Get("X-GitHub-Event") == "ping" {
		return map[string]bool{"accepted": true}, nil
	}
	if r.Header.Get("X-GitHub-Event") != "issues" {
		return map[string]bool{"ignored": true}, nil
	}
	var payload webhookPayload
	if json.Unmarshal(body, &payload) != nil || payload.Issue.Number <= 0 {
		return nil, &apiError{422, "invalid_input", "Invalid issue webhook"}
	}
	return map[string]bool{"accepted": true}, s.ingestWebhook(r, payload, delivery)
}
func (s *Server) ingestWebhook(r *http.Request, v webhookPayload, delivery string) error {
	projects, err := s.Store.List(r.Context(), "projects")
	if err != nil {
		return &apiError{503, "unavailable", err.Error()}
	}
	for _, record := range projects {
		p, _ := store.Decode[model.Project](record)
		if p.Repo == v.Repository.Name {
			return s.ingestProjectWebhook(r, p, v.issue(), delivery)
		}
	}
	return &apiError{404, "project_not_found", "Repository not onboarded"}
}
func (s *Server) ingestProjectWebhook(r *http.Request, p model.Project, i model.Issue, delivery string) error {
	var err error
	if p.DefaultBranch != "" {
		i, err = s.Engine.GitHub.Issue(r.Context(), p.Repo, i.Number)
		if err != nil {
			return &apiError{503, "github_unavailable", err.Error()}
		}
	}
	if _, err = s.Engine.Ingest(r.Context(), p, i); err != nil && !errors.Is(err, store.ErrNotFound) {
		return &apiError{503, "unavailable", err.Error()}
	}
	if _, err = s.Store.Delivery(r.Context(), delivery); err != nil {
		return &apiError{503, "unavailable", err.Error()}
	}
	return nil
}
