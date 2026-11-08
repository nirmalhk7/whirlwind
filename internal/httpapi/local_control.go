package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
)

type LocalCommand struct {
	Operation       string
	ProjectID       string
	PlanID          string
	ResourceID      string
	ExpectedVersion int
	DiffOffset      int64
	ContentHash     string
	IdempotencyKey  string
	Payload         json.RawMessage
}

const localTaskRoutePrefix = "/api/v1/tasks/"

func localPayload(payload json.RawMessage) json.RawMessage {
	if len(payload) == 0 {
		return json.RawMessage(`{}`)
	}
	return payload
}

// ExecuteLocal exposes only named operator decisions through the existing
// versioned and idempotent domain handlers. It starts no HTTP listener.
func (s *Server) ExecuteLocal(ctx context.Context, command LocalCommand) (json.RawMessage, error) {
	switch command.Operation {
	case "project.register":
		return s.registerLocalProject(ctx, command)
	case "finalization.inspect":
		return s.inspectFinalization(ctx, command)
	case "finalization.approve":
		return s.approveLocalFinalization(ctx, command)
	case "inspect.artifact", "provider.status":
		return s.localInspection(ctx, command)
	case "question.answer", "permission.decide":
		return s.executeHumanDecision(ctx, command)
	default:
		return s.executeNamedDomainRoute(ctx, command)
	}
}

func (s *Server) executeNamedDomainRoute(ctx context.Context, command LocalCommand) (json.RawMessage, error) {
	method, path, payload, err := localRoute(command)
	if err != nil {
		return nil, err
	}
	request := httptest.NewRequest(method, "http://127.0.0.1"+path, bytes.NewReader(payload)).WithContext(ctx)
	request.Header.Set("Idempotency-Key", command.IdempotencyKey)
	if command.ExpectedVersion > 0 {
		request.Header.Set("If-Match", fmt.Sprintf("%d", command.ExpectedVersion))
	}
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, request)
	if response.Code < 200 || response.Code >= 300 {
		return nil, localResponseError(response)
	}
	body := bytes.TrimSpace(response.Body.Bytes())
	if len(body) == 0 {
		return json.RawMessage("null"), nil
	}
	if !json.Valid(body) {
		return nil, errors.New("local domain handler returned invalid JSON")
	}
	return append(json.RawMessage(nil), body...), nil
}

func localRoute(command LocalCommand) (string, string, []byte, error) {
	payload := localPayload(command.Payload)
	switch command.Operation {
	case "inspect.plan":
		return localTaskRoute(command.PlanID, "plan-document", http.MethodGet, nil)
	case "project.register":
		return http.MethodPost, "/api/v1/projects", payload, nil
	case "ideation.start":
		return localProjectRoute(command.ProjectID, "ideation", payload)
	case "direction.select", "direction.approve":
		return localDirectionRoute(command.ResourceID, "select", payload)
	case "direction.reject":
		return localDirectionRoute(command.ResourceID, "dismiss", payload)
	case "direction.reconsider":
		var input struct {
			ProjectID string `json:"project_id"`
			Reason    string `json:"reason"`
		}
		if err := json.Unmarshal(payload, &input); err != nil {
			return "", "", nil, err
		}
		if command.ProjectID == "" || input.ProjectID != command.ProjectID {
			return "", "", nil, errors.New("direction reconsideration project does not match local command")
		}
		return localDirectionRoute(command.ResourceID, "reconsider", payload)
	case "plan.review":
		return localPlanReviewRoute(command, payload)
	case "plan.pause", "plan.resume":
		return localTaskActionRoute(command, payload)
	default:
		return "", "", nil, fmt.Errorf("unsupported local operator command %q", command.Operation)
	}
}

func localID(value string) (string, error) {
	if strings.TrimSpace(value) == "" || strings.Contains(value, "/") || value == "." || value == ".." {
		return "", errors.New("local command resource ID is required")
	}
	return url.PathEscape(value), nil
}

func localTaskRoute(id, action, method string, payload []byte) (string, string, []byte, error) {
	escaped, err := localID(id)
	if err != nil {
		return "", "", nil, err
	}
	return method, localTaskRoutePrefix + escaped + "/" + action, payload, nil
}

func localProjectRoute(id, action string, payload []byte) (string, string, []byte, error) {
	project, err := localID(id)
	if err != nil {
		return "", "", nil, err
	}
	return http.MethodPost, "/api/v1/projects/" + project + "/" + action, payload, nil
}

func localDirectionRoute(id, action string, payload []byte) (string, string, []byte, error) {
	direction, err := localID(id)
	if err != nil {
		return "", "", nil, err
	}
	return http.MethodPost, "/api/v1/directions/" + direction + "/" + action, payload, nil
}

func localPlanReviewRoute(command LocalCommand, payload []byte) (string, string, []byte, error) {
	if _, err := localID(command.PlanID); err != nil {
		return "", "", nil, err
	}
	var input struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(payload, &input); err != nil {
		return "", "", nil, err
	}
	if command.ContentHash != "" && input.Hash != command.ContentHash {
		return "", "", nil, errors.New("plan review hash does not match inspected content")
	}
	return localTaskRoute(command.PlanID, "plan-review", http.MethodPost, payload)
}

func localTaskActionRoute(command LocalCommand, payload []byte) (string, string, []byte, error) {
	action := strings.TrimPrefix(command.Operation, "plan.")
	return localTaskRoute(command.PlanID, action, http.MethodPost, payload)
}

func localResponseError(response *httptest.ResponseRecorder) error {
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(response.Body.Bytes(), &envelope) == nil && envelope.Error.Message != "" {
		return fmt.Errorf("%s: %s", envelope.Error.Code, envelope.Error.Message)
	}
	return fmt.Errorf("local domain command failed with status %d", response.Code)
}
