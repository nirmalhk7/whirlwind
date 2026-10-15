package provider

import (
	"context"
	"encoding/json"
	"strings"
	"whirlwind/internal/model"
)

func (s *providerSession) codexQuestions(c context.Context, m Message) (any, error) {
	var request struct {
		Questions []struct {
			ID      string `json:"id"`
			Text    string `json:"question"`
			Secret  bool   `json:"isSecret"`
			Options []struct {
				Label string `json:"label"`
			} `json:"options"`
		} `json:"questions"`
	}
	if e := json.Unmarshal(m.Params, &request); e != nil {
		return nil, e
	}
	questions := []model.Question{}
	for _, q := range request.Questions {
		x := model.Question{ID: q.ID, Text: q.Text, Secret: q.Secret}
		for _, o := range q.Options {
			x.Options = append(x.Options, o.Label)
		}
		questions = append(questions, x)
	}
	return AnswerQuestions(c, s.hooks, questions, "codex")
}

func (s *providerSession) claudeQuestions(c context.Context, m Message) (any, error) {
	var request struct {
		Questions []model.Question `json:"questions"`
	}
	if e := json.Unmarshal(m.Params, &request); e != nil {
		return nil, e
	}
	return AnswerQuestions(c, s.hooks, request.Questions, "claude")
}

func (s *providerSession) requestPermission(c context.Context, m Message, p map[string]any) (any, error) {
	permission := parsePermission(m, p)
	allowed := false
	var e error
	if s.hooks.Permission != nil {
		allowed, e = s.hooks.Permission(c, permission)
		if e != nil {
			return nil, e
		}
	}
	return permissionResponse(m.Method, p, allowed), nil
}

func parsePermission(m Message, p map[string]any) Permission {
	permission := Permission{Tool: m.Method, Raw: m.Params}
	if command, ok := p["command"].(string); ok && !strings.ContainsAny(command, ";&|><`$\\\"'()\n") {
		permission.Command = strings.Fields(command)
	}
	if input, ok := p["input"].(map[string]any); ok {
		if path, ok := input["file_path"].(string); ok {
			permission.Paths = append(permission.Paths, path)
		}
		if command, ok := input["command"].(string); ok && !strings.ContainsAny(command, ";&|><`$\\\"'()\n") {
			permission.Command = strings.Fields(command)
		}
	}
	permission.Command = append(permission.Command, permissionStrings(p["command"])...)
	permission.Paths = append(permission.Paths, permissionStrings(p["paths"])...)
	return permission
}

func permissionResponse(method string, p map[string]any, allowed bool) any {
	switch method {
	case "session/request_permission":
		options, _ := p["options"].([]any)
		want := "reject_once"
		if allowed {
			want = "allow_once"
		}
		for _, v := range options {
			o, _ := v.(map[string]any)
			if o["kind"] == want {
				return map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": o["optionId"]}}
			}
		}
		return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}
	case "permission/request":
		return map[string]any{"allow": allowed}
	default:
		decision := "decline"
		if allowed {
			decision = "accept"
		}
		return map[string]any{"decision": decision}
	}
}

func permissionStrings(value any) []string {
	values, _ := value.([]any)
	var result []string
	for _, value := range values {
		if text, ok := value.(string); ok {
			result = append(result, text)
		}
	}
	return result
}
