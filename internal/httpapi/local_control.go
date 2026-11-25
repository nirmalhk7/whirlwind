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

	"whirlwind/internal/control"
)

// LocalCommand is the command received from the supervisor's local protocol.
type LocalCommand = control.Request

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
	case control.RoleQueuePushOperation, control.RoleQueuePopOperation, "runners.toggle":
		return s.executeRunnerControl(ctx, command)
	case "ideation.start":
		return s.executeIdeationStart(ctx, command)
	case "plan.pause", "plan.resume":
		return s.executePlanActivity(ctx, command)
	case "idea.reconsider":
		return s.executeIdeaReconsider(ctx, command)
	case "planner.cancel":
		return s.executePlannerCancel(ctx, command)
	case "idea.update":
		return s.executeIdeaUpdate(ctx, command)
	case "idea.approve", "idea.archive", "idea.retry_planning":
		return s.executeIdeaDecision(ctx, command)
	case "inspect.plan", "plan.document.read", "plan.document.save", "plan.approve", "plan.request_changes", "plan.reject", "plan.review":
		return s.executePlanCommand(ctx, command)
	case "project.register":
		return s.registerLocalProject(ctx, command)
	case "project.setup.retry":
		return s.retryLocalProjectSetup(ctx, command)
	case "project.policy.preview", "project.policy.approve", "inspect.memory", "memory.remember", "memory.clear":
		return s.executeLocalProjectData(ctx, command)
	case "finalization.inspect":
		return s.inspectFinalization(ctx, command)
	case "finalization.approve":
		return s.approveLocalFinalization(ctx, command)
	case "inspect.artifact", "provider.status":
		return s.localInspection(ctx, command)
	case "question.answer", "permission.decide":
		return s.executeHumanDecision(ctx, command)
	default:
		return s.executeLegacyDirectionRoute(ctx, command)
	}
}

func (s *Server) executeLegacyDirectionRoute(ctx context.Context, command LocalCommand) (json.RawMessage, error) {
	method, path, payload, err := legacyDirectionRoute(command)
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

func legacyDirectionRoute(command LocalCommand) (string, string, []byte, error) {
	payload := localPayload(command.Payload)
	switch command.Operation {
	case "direction.select", "direction.approve":
		return localDirectionRoute(command.ResourceID, "select", payload)
	case "direction.delete":
		direction, err := localID(command.ResourceID)
		if err != nil {
			return "", "", nil, err
		}
		return http.MethodDelete, "/api/v1/directions/" + direction, payload, nil
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

func localDirectionRoute(id, action string, payload []byte) (string, string, []byte, error) {
	direction, err := localID(id)
	if err != nil {
		return "", "", nil, err
	}
	return http.MethodPost, "/api/v1/directions/" + direction + "/" + action, payload, nil
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
