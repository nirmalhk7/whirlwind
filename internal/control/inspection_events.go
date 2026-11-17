package control

import (
	"context"
	"encoding/json"
	"errors"

	"whirlwind/internal/model"
)

func (d SupervisorDispatcher) events(ctx context.Context, request Request) (events []model.Event, resultErr error) {
	if d.DB == nil {
		return nil, errors.New(stateDatabaseUnavailable)
	}
	limit := request.Limit
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 500 {
		return nil, errors.New("event page limit must be between 1 and 500")
	}
	pageBytes := 128
	rows, err := d.DB.QueryContext(ctx, "SELECT id,kind,resource_id,data,created_at FROM events WHERE id>? ORDER BY id LIMIT ?", request.EventCursor, limit+1)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); resultErr == nil {
			resultErr = closeErr
		}
	}()
	for rows.Next() {
		var event model.Event
		var data []byte
		if err := rows.Scan(&event.ID, &event.Kind, &event.ResourceID, &data, &event.CreatedAt); err != nil {
			return nil, err
		}
		event.Data = data
		encoded, err := json.Marshal(event)
		if err != nil {
			return nil, err
		}
		rowBytes := len(encoded) + 1
		if rowBytes+1024 > maxInspectionPageBytes {
			if len(events) == 0 {
				return nil, errors.New("inspection event exceeds safe local page size")
			}
			break
		}
		if len(events) == limit || pageBytes+rowBytes > maxInspectionPageBytes-1024 {
			break
		}
		events = append(events, event)
		pageBytes += rowBytes
	}
	return events, rows.Err()
}
