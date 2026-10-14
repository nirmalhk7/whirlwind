package provider

import (
	"context"
	"errors"
	"whirlwind/internal/model"
	"strings"
)

// AnswerQuestions validates non-secret agent questions, waits for user answers through the hook, and encodes the provider-specific response.
func AnswerQuestions(ctx context.Context, h Hooks, questions []model.Question, kind string) (any, error) {
	if h.Questions == nil {
		return nil, errors.New("user question handler unavailable")
	}
	if len(questions) == 0 {
		return nil, errors.New("questions required")
	}
	seen := map[string]bool{}
	for _, q := range questions {
		if seen[q.ID] {
			return nil, errors.New("duplicate question id")
		}
		seen[q.ID] = true
		if q.ID == "" || q.Text == "" {
			return nil, errors.New("question id and text required")
		}
		if q.Secret {
			return nil, errors.New("configure secrets through provider login or project environment")
		}
	}
	answers, e := h.Questions(ctx, questions)
	if e != nil {
		return nil, e
	}
	for _, q := range questions {
		if strings.TrimSpace(answers[q.ID]) == "" {
			return nil, errors.New("all native questions require answers")
		}
	}
	if kind == "codex" {
		encoded := map[string]any{}
		for id, answer := range answers {
			encoded[id] = map[string]any{"answers": []string{answer}}
		}
		return map[string]any{"answers": encoded}, nil
	}
	return map[string]any{"answers": answers}, nil
}
