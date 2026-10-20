package httpapi

func (q apiRequest) providerStatus() error {
	statuses, err := q.Engine.ProviderStatuses(q.r.Context())
	if err != nil {
		return err
	}
	reply(q.w, 200, statuses)
	return nil
}
