package httpapi

import (
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (q apiRequest) clearInbox() error {
	if q.id != "clear" {
		return &apiError{404, "route_not_found", "Unsupported route"}
	}
	records, err := q.Store.List(q.r.Context(), "inbox")
	if err != nil {
		return err
	}
	changes := []store.Change{}
	for _, record := range records {
		notification, err := store.Decode[model.Notification](record)
		if err != nil {
			return err
		}
		if notification.Resolved {
			continue
		}
		notification.Resolved = true
		changes = append(changes, store.Change{Kind: "inbox", ID: record.ID, Value: notification, Expected: record.Version})
	}
	if err := q.Store.Write(q.r.Context(), changes, nil); err != nil {
		return err
	}
	reply(q.w, 200, map[string]int{"resolved": len(changes)})
	return nil
}
