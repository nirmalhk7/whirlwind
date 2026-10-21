package httpapi

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"whirlwind/internal/model"
)

func (q apiRequest) lessons() error {
	if q.id != "" {
		record, err := q.Store.Get(q.r.Context(), "lessons", q.id)
		if err != nil {
			return err
		}
		var lesson model.Lesson
		if err = json.Unmarshal(record.Data, &lesson); err != nil {
			return err
		}
		if _, err = q.Store.Get(q.r.Context(), "projects", lesson.ProjectID); err != nil {
			return err
		}
		if q.r.Method == "POST" {
			version, err := expected(q.r)
			if err != nil {
				return &apiError{428, "precondition_required", err.Error()}
			}
			record, err = q.Store.RetireLesson(q.r.Context(), q.id, version)
			if err != nil {
				return err
			}
		}
		reply(q.w, 200, record)
		return nil
	}
	query := q.r.URL.Query()
	project := query.Get("project_id")
	if project == "" {
		return errors.New("project_id required")
	}
	if _, err := q.Store.Get(q.r.Context(), "projects", project); err != nil {
		return err
	}
	role, status := query.Get("role"), query.Get("status")
	if role != "" && !slices.Contains(model.AgentRoles(), role) {
		return errors.New("unknown agent role")
	}
	if status != "" && !slices.Contains([]string{"tentative", "corroborated", "stale", "retired"}, status) {
		return errors.New("unknown lesson status")
	}
	after := int64(0)
	limit := 100
	var err error
	if value := query.Get("after"); value != "" {
		after, err = strconv.ParseInt(value, 10, 64)
		if err != nil || after < 0 || after > 9007199254740991 {
			return errors.New("after must be a non-negative safe integer")
		}
	}
	if value := query.Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 500 {
			return errors.New("limit must be 1 through 500")
		}
	}
	page, err := q.Store.LessonsPage(q.r.Context(), project, role, status, after, limit)
	if err != nil {
		return err
	}
	reply(q.w, 200, page)
	return nil
}
