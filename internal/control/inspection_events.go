package control

import (
	"context"
	"database/sql"
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
	rows, err := d.DB.QueryContext(ctx, "SELECT id,kind,resource_id,data,created_at FROM events WHERE id>? ORDER BY id LIMIT ?", request.EventCursor, limit+1)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); resultErr == nil {
			resultErr = closeErr
		}
	}()
	return readInspectionEvents(rows, limit)
}

func readInspectionEvents(rows *sql.Rows, limit int) ([]model.Event, error) {
	var events []model.Event
	pageBytes := initialInspectionPageSize
	for rows.Next() {
		event, err := scanInspectionEvent(rows)
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(event)
		if err != nil {
			return nil, err
		}
		rowBytes := len(encoded) + 1
		if len(events) == 0 && rowBytes+inspectionPageHeadroom > maxInspectionPageBytes {
			return nil, errors.New("inspection event exceeds safe local page size")
		}
		if len(events) == limit || pageBytes+rowBytes > maxInspectionPageBytes-inspectionPageHeadroom {
			break
		}
		events = append(events, event)
		pageBytes += rowBytes
	}
	return events, rows.Err()
}

func scanInspectionEvent(rows *sql.Rows) (model.Event, error) {
	var event model.Event
	var data []byte
	err := rows.Scan(&event.ID, &event.Kind, &event.ResourceID, &data, &event.CreatedAt)
	event.Data = data
	return event, err
}
