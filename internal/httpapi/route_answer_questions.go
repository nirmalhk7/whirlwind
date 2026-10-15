package httpapi

import (
	"encoding/json"
	"strings"
	"whirlwind/internal/model"
)

func (q apiRequest) answerQuestions() error {
	n, x := q.expected()
	if x != nil {
		return x
	}
	set, _, x := readRecord[model.QuestionSet](q, "questions")
	if x != nil {
		return x
	}
	answers, x := nativeAnswers(set, q.body)
	if x != nil {
		return x
	}
	set.Answers = answers
	set.Status = "answered"
	if err := q.Store.Audit(q.r.Context(), model.Audit{Task: set.TaskID, AttemptID: set.AttemptID, Source: "local_operator", Action: "answer_questions", Target: q.id, Version: n}); err != nil {
		return err
	}
	q.save(q.w, q.r, "questions", q.id, set, n, 200)
	return nil
}

func nativeAnswers(set model.QuestionSet, body []byte) (map[string]string, error) {
	if set.Status != "pending" {
		return nil, &apiError{409, "invalid_stage", "Question is no longer pending"}
	}
	var input struct {
		Answers map[string]string `json:"answers"`
	}
	if x := json.Unmarshal(body, &input); x != nil {
		return nil, x
	}
	for _, q := range set.Questions {
		if strings.TrimSpace(input.Answers[q.ID]) == "" {
			return nil, &apiError{422, "invalid_input", "Answer every question"}
		}
	}
	return input.Answers, nil
}
