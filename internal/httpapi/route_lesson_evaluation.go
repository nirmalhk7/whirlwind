package httpapi

import (
	"encoding/json"
	"whirlwind/internal/workflow"
)

func (q apiRequest) evaluateLessonSearch() error {
	project, err := q.Engine.Project(q.r.Context(), q.id)
	if err != nil {
		return err
	}
	if project.Policy.Embeddings == nil || !project.Policy.Embeddings.LessonsEnabled {
		return &apiError{422, "invalid_input", "Enable embeddings.lessons_enabled with an explicit endpoint and model in committed policy"}
	}
	var input struct {
		Fixtures []workflow.LessonFixture `json:"fixtures"`
	}
	if err = json.Unmarshal(q.body, &input); err != nil {
		return err
	}
	result, err := q.Engine.EvaluateLessonSearch(q.r.Context(), project, input.Fixtures)
	if err == nil {
		reply(q.w, 200, result)
	}
	return err
}
