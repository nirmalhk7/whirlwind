package httpapi

import (
	"encoding/json"
	"whirlwind/internal/model"
)

func (q apiRequest) lessons() error {
	if q.id != "" {
		return q.lessonItem()
	}
	return q.lessonList()
}

func (q apiRequest) lessonItem() error {
	record, err := q.registeredLesson()
	if err != nil {
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

func (q apiRequest) registeredLesson() (model.Record, error) {
	record, err := q.Store.Get(q.r.Context(), "lessons", q.id)
	if err != nil {
		return record, err
	}
	var lesson model.Lesson
	if err := json.Unmarshal(record.Data, &lesson); err != nil {
		return record, err
	}
	_, err = q.Store.Get(q.r.Context(), "projects", lesson.ProjectID)
	return record, err
}

func (q apiRequest) lessonList() error {
	query, err := q.lessonQuery()
	if err != nil {
		return err
	}
	page, err := q.Store.LessonsPage(q.r.Context(), query.project, query.role, query.status, query.after, query.limit)
	if err != nil {
		return err
	}
	reply(q.w, 200, page)
	return nil
}
