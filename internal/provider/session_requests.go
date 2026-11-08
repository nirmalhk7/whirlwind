package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	if s.nativeReadPermission(p) {
		return permissionResponse(m.Method, p, true), nil
	}
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

// mcpElicitation routes native authentication and flat forms through existing
// operator hooks. Credentials remain in provider login, never persisted questions.
func (s *providerSession) mcpElicitation(ctx context.Context, m Message, p map[string]any) (any, error) {
	if ctx.Err() != nil {
		return map[string]any{"action": "cancel"}, nil
	}
	if p["mode"] == "url" || p["url"] != nil {
		return s.mcpURLElicitation(ctx, m, p)
	}
	if mode, _ := p["mode"].(string); mode != "" && mode != "form" {
		return s.declineElicitation("Unsupported MCP elicitation mode"), nil
	}
	return s.mcpFormElicitation(ctx, p)
}

func (s *providerSession) mcpURLElicitation(ctx context.Context, m Message, p map[string]any) (any, error) {
	allowed := false
	var err error
	if s.hooks.Permission != nil {
		allowed, err = s.hooks.Permission(ctx, parsePermission(m, p))
	}
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return map[string]any{"action": "cancel"}, nil
	}
	return permissionResponse(m.Method, p, allowed), err
}

func (s *providerSession) mcpFormElicitation(ctx context.Context, p map[string]any) (any, error) {
	fields, questions, required, err := elicitationQuestions(p["requestedSchema"])
	if err != nil {
		return s.declineElicitation(err.Error()), nil
	}
	answers, err := elicitationAnswers(ctx, s.hooks, questions)
	if errors.Is(err, context.Canceled) {
		return map[string]any{"action": "cancel"}, nil
	}
	if err != nil {
		return s.declineElicitation(err.Error()), nil
	}
	form := elicitationForm{fields: fields, questions: questions, required: required}
	content, err := form.content(ctx, answers)
	if err != nil {
		return s.declineElicitation(err.Error()), nil
	}
	return map[string]any{"action": "accept", "content": content}, nil
}

func (s *providerSession) declineElicitation(reason string) map[string]any {
	if s.hooks.Event != nil {
		s.hooks.Event("elicitation_declined", map[string]string{"reason": reason})
	}
	return map[string]any{"action": "decline"}
}

func elicitationAnswers(ctx context.Context, hooks Hooks, questions []model.Question) (map[string]string, error) {
	if hooks.Questions == nil {
		return nil, errors.New("user question handler unavailable")
	}
	if err := validateQuestions(questions); err != nil {
		return nil, err
	}
	answers, err := hooks.Questions(ctx, questions)
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return nil, context.Canceled
	}
	if err != nil {
		return nil, errors.New("user question could not be answered")
	}
	return answers, nil
}

func (form elicitationForm) content(ctx context.Context, answers map[string]string) (map[string]any, error) {
	answered := []model.Question{}
	for _, question := range form.questions {
		if form.required[question.ID] || strings.TrimSpace(answers[question.ID]) != "" {
			answered = append(answered, question)
		}
	}
	// Optional absent fields are excluded before normal native-answer validation.
	if len(answered) > 0 {
		hooks := Hooks{Questions: func(context.Context, []model.Question) (map[string]string, error) { return answers, nil }}
		if _, err := AnswerQuestions(ctx, hooks, answered, "mcp"); err != nil {
			return nil, err
		}
	}
	content := map[string]any{}
	for _, question := range answered {
		value, err := elicitationValue(form.fields[question.ID], answers[question.ID])
		if err != nil {
			return nil, fmt.Errorf("invalid answer for %s: %s", question.ID, err.Error())
		}
		content[question.ID] = value
	}
	return content, nil
}
