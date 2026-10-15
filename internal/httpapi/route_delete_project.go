package httpapi

func (q apiRequest) deleteProject() error {
	if q.id == "" {
		return &apiError{404, "route_not_found", "Project ID required"}
	}
	record, err := q.currentRecord("projects")
	if err != nil {
		return err
	}
	if err = q.Store.UnregisterProject(q.r.Context(), q.id, record.Version); err != nil {
		return err
	}
	reply(q.w, 200, map[string]any{"id": q.id, "deleted": true})
	return nil
}
