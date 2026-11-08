package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"whirlwind/internal/model"
)

const (
	inspectOperationPrefix   = "inspect."
	stateDatabaseUnavailable = "supervisor state database unavailable"
	maxInspectionPageBytes   = 512 << 10
)

var inspectableKinds = map[string]bool{
	"projects": true, "tasks": true, "directions": true, "sessions": true,
	"questions": true, "permissions": true, "verification": true,
	"reviews": true, "inbox": true, "plans": true, "artifacts": true, "agents": true, "project_status": true,
}

type CommandExecutor interface {
	Execute(context.Context, Request) (any, error)
}

type SupervisorDispatcher struct {
	DB       *sql.DB
	Commands CommandExecutor
}

type RecordPage struct {
	Kind       string         `json:"kind"`
	ProjectID  string         `json:"project_id,omitempty"`
	Cursor     int64          `json:"cursor"`
	NextCursor int64          `json:"next_cursor"`
	HasMore    bool           `json:"has_more"`
	Records    []model.Record `json:"records"`
}

func (d SupervisorDispatcher) Handle(ctx context.Context, request Request) (Response, error) {
	var result any
	var err error
	switch request.Operation {
	case "inspect.snapshot":
		result, err = d.snapshot(ctx, request)
	case "inspect.records":
		result, err = d.records(ctx, request)
	case "inspect.events":
		result, err = d.events(ctx, request)
	default:
		if !allowedOperations[request.Operation] || (strings.HasPrefix(request.Operation, inspectOperationPrefix) && request.Operation != "inspect.plan" && request.Operation != "inspect.artifact") {
			return Response{}, fmt.Errorf("unsupported local operation %q", request.Operation)
		}
		if d.Commands == nil {
			return Response{}, errors.New("supervisor command adapter unavailable")
		}
		result, err = d.Commands.Execute(ctx, request)
	}
	return Response{RequestID: request.RequestID, Result: result}, err
}

func (d SupervisorDispatcher) records(ctx context.Context, request Request) (page RecordPage, resultErr error) {
	if d.DB == nil {
		return RecordPage{}, errors.New(stateDatabaseUnavailable)
	}
	kind := request.ResourceID
	if !inspectableKinds[kind] {
		return RecordPage{}, fmt.Errorf("resource kind %q is not available through local inspection", kind)
	}
	limit := request.Limit
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 100 {
		return RecordPage{}, errors.New("inspection page limit must be between 1 and 100")
	}
	page = RecordPage{Kind: kind, ProjectID: request.ProjectID, Cursor: request.EventCursor, Records: make([]model.Record, 0, limit)}
	pageBytes := 128
	query := "SELECT rowid,id,version,data,updated_at FROM resources WHERE kind=? AND rowid>? AND (?='' OR json_extract(data,'$.project_id')=?) ORDER BY rowid LIMIT ?"
	rows, err := d.DB.QueryContext(ctx, query, kind, request.EventCursor, request.ProjectID, request.ProjectID, limit+1)
	if err != nil {
		return page, err
	}
	defer func() {
		if closeErr := rows.Close(); resultErr == nil {
			resultErr = closeErr
		}
	}()
	for rows.Next() {
		var record model.Record
		var data []byte
		if err := rows.Scan(&record.CreatedOrder, &record.ID, &record.Version, &data, &record.UpdatedAt); err != nil {
			return page, err
		}
		record.Data = data
		encoded, err := json.Marshal(record)
		if err != nil {
			return page, err
		}
		rowBytes := len(encoded) + 1
		if rowBytes+1024 > maxInspectionPageBytes {
			if len(page.Records) == 0 {
				return page, errors.New("inspection record exceeds safe local page size")
			}
			page.HasMore = true
			break
		}
		if len(page.Records) == limit {
			page.HasMore = true
			break
		}
		if pageBytes+rowBytes > maxInspectionPageBytes-1024 {
			page.HasMore = true
			break
		}
		page.Records = append(page.Records, record)
		page.NextCursor = record.CreatedOrder
		pageBytes += rowBytes
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	return page, nil
}

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

func (d SupervisorDispatcher) snapshot(ctx context.Context, request Request) (Snapshot, error) {
	if d.DB == nil {
		return nil, errors.New("supervisor state database unavailable")
	}
	snapshot := Snapshot{}
	for _, kind := range []string{"projects", "tasks", "directions", "sessions", "questions", "permissions", "verification", "reviews", "inbox", "plans", "agents", "project_status"} {
		page, err := d.records(ctx, Request{ResourceID: kind, ProjectID: request.ProjectID, Limit: 100})
		if err != nil {
			return nil, err
		}
		snapshot[kind] = page.Records
	}
	events, err := d.events(ctx, Request{EventCursor: request.EventCursor, Limit: 100})
	if err != nil {
		return nil, err
	}
	for _, event := range events {
		data, err := json.Marshal(event)
		if err != nil {
			return nil, err
		}
		snapshot["events"] = append(snapshot["events"], model.Record{ID: strconv.FormatInt(event.ID, 10), Data: data, UpdatedAt: event.CreatedAt})
	}
	return snapshot, nil
}

// Snapshot exposes a read-only, paginated snapshot suitable for offline dashboard inspection.
func (d SupervisorDispatcher) Snapshot(ctx context.Context, limit int) (Snapshot, error) {
	if limit < 1 || limit > 100 {
		return nil, errors.New("snapshot limit must be between 1 and 100")
	}
	snapshot := Snapshot{}
	for _, kind := range []string{"projects", "tasks", "directions", "sessions", "questions", "permissions", "verification", "reviews", "inbox", "plans", "agents", "project_status"} {
		records, err := d.allRecords(ctx, kind, limit)
		if err != nil {
			return nil, err
		}
		snapshot[kind] = records
	}
	events, err := d.allEvents(ctx, limit)
	if err != nil {
		return nil, err
	}
	for _, event := range events {
		data, err := json.Marshal(event)
		if err != nil {
			return nil, err
		}
		snapshot["events"] = append(snapshot["events"], model.Record{ID: strconv.FormatInt(event.ID, 10), Data: data, UpdatedAt: event.CreatedAt})
	}
	return snapshot, nil
}

func (d SupervisorDispatcher) allRecords(ctx context.Context, kind string, limit int) ([]model.Record, error) {
	var records []model.Record
	var cursor int64
	for {
		page, err := d.records(ctx, Request{ResourceID: kind, EventCursor: cursor, Limit: limit})
		if err != nil {
			return nil, err
		}
		records = append(records, page.Records...)
		if !page.HasMore {
			return records, nil
		}
		if page.NextCursor <= cursor {
			return nil, fmt.Errorf("inspection cursor did not advance for %s", kind)
		}
		cursor = page.NextCursor
	}
}

func (d SupervisorDispatcher) allEvents(ctx context.Context, limit int) ([]model.Event, error) {
	var events []model.Event
	var cursor int64
	for {
		page, err := d.events(ctx, Request{EventCursor: cursor, Limit: limit})
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			return events, nil
		}
		if page[len(page)-1].ID <= cursor {
			return nil, errors.New("event inspection cursor did not advance")
		}
		events = append(events, page...)
		cursor = page[len(page)-1].ID
	}
}
