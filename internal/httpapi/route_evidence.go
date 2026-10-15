package httpapi

import (
	"strconv"
)

func (q apiRequest) attemptEvents() error {
	after := int64(0)
	limit := 100
	var err error
	if raw := q.r.URL.Query().Get("after"); raw != "" {
		after, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || after < 0 || after > 9007199254740991 {
			return &apiError{422, "invalid_cursor", "Non-negative safe integer cursor required"}
		}
	}
	if raw := q.r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 500 {
			return &apiError{422, "invalid_limit", "Limit must be between 1 and 500"}
		}
	}
	items, err := q.Store.AttemptEvents(q.r.Context(), q.id, after, limit)
	if err != nil {
		return err
	}
	next := after
	if len(items) > 0 {
		next = items[len(items)-1].Sequence
	}
	reply(q.w, 200, map[string]any{"items": items, "next_after": next})
	return nil
}
func (q apiRequest) artifact() error {
	if q.action == "content" {
		a, content, err := q.Store.Artifact(q.r.Context(), q.id)
		if err != nil {
			return err
		}
		q.w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		q.w.Header().Set("X-Evidence-Truncated", strconv.FormatBool(a.Truncated))
		q.w.WriteHeader(200)
		_, err = q.w.Write(content)
		return err
	}
	a, err := q.Store.ArtifactMetadata(q.r.Context(), q.id)
	if err != nil {
		return err
	}
	reply(q.w, 200, a)
	return nil
}

func (q apiRequest) linkedEvidence() error {
	items, err := q.Store.LinkedEvidence(q.r.Context(), q.id)
	if err != nil {
		return err
	}
	reply(q.w, 200, map[string]any{"items": items})
	return nil
}
