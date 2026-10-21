package httpapi

import (
	"errors"
	"net/url"
	"slices"
	"strconv"
	"whirlwind/internal/model"
)

type lessonQuery struct {
	project, role, status string
	after                 int64
	limit                 int
}

func (q apiRequest) lessonQuery() (lessonQuery, error) {
	values := q.r.URL.Query()
	query := lessonQuery{project: values.Get("project_id"), role: values.Get("role"), status: values.Get("status")}
	if query.project == "" {
		return query, errors.New("project_id required")
	}
	if _, err := q.Store.Get(q.r.Context(), "projects", query.project); err != nil {
		return query, err
	}
	if err := query.validateFilters(); err != nil {
		return query, err
	}
	var err error
	query.after, query.limit, err = lessonPagination(values)
	return query, err
}

func (q lessonQuery) validateFilters() error {
	if q.role != "" && !slices.Contains(model.AgentRoles(), q.role) {
		return errors.New("unknown agent role")
	}
	if q.status != "" && !slices.Contains([]string{"tentative", "corroborated", "stale", "retired"}, q.status) {
		return errors.New("unknown lesson status")
	}
	return nil
}

func lessonPagination(values url.Values) (int64, int, error) {
	after := int64(0)
	limit := 100
	var err error
	if value := values.Get("after"); value != "" {
		after, err = strconv.ParseInt(value, 10, 64)
		if err != nil || after < 0 || after > 9007199254740991 {
			return 0, 0, errors.New("after must be a non-negative safe integer")
		}
	}
	if value := values.Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 500 {
			return 0, 0, errors.New("limit must be 1 through 500")
		}
	}
	return after, limit, nil
}
