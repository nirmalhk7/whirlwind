package provider

import (
	"context"
	"errors"
	"strings"
	"whirlwind/internal/model"
)

// AnswerQuestions validates non-secret agent questions, waits for user answers through the hook, and encodes the provider-specific response.
func AnswerQuestions(ctx context.Context, h Hooks, questions []model.Question, kind string) (any, error) {
	if h.Questions == nil {
		return nil, errors.New("user question handler unavailable")
	}
	if err := validateQuestions(questions); err != nil {
		return nil, err
	}
	answers, e := h.Questions(ctx, questions)
	if e != nil {
		return nil, e
	}
	return questionResponse(questions, answers, kind)
}

func validateQuestions(questions []model.Question) error {
	if len(questions) == 0 {
		return errors.New("questions required")
	}
	seen := map[string]bool{}
	for _, q := range questions {
		if seen[q.ID] {
			return errors.New("duplicate question id")
		}
		seen[q.ID] = true
		if q.ID == "" || q.Text == "" {
			return errors.New("question id and text required")
		}
		if q.Secret {
			return errors.New("configure secrets through provider login or project environment")
		}
	}
	return nil
}

func questionResponse(questions []model.Question, answers map[string]string, kind string) (any, error) {
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
