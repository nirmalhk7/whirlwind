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
	"whirlwind/internal/store"
)

const (
	inspectOperationPrefix    = "inspect."
	stateDatabaseUnavailable  = "supervisor state database unavailable"
	maxInspectionPageBytes    = 512 << 10
	inspectionPageHeadroom    = 1024
	initialInspectionPageSize = 128
	inspectionRecordTooLarge  = "inspection record exceeds safe local page size"
)

var inspectableKinds = map[string]bool{
	"projects": true, "execution_plans": true, "ideas": true, "operations": true, "directions": true, "sessions": true,
	"questions": true, "permissions": true, "verification": true,
	"reviews": true, "inbox": true, "plan_versions": true, "artifacts": true, "agents": true, "providers": true, "project_status": true, "settings": true,
}

type CommandExecutor interface {
	Execute(context.Context, Request) (any, error)
}

type SupervisorDispatcher struct {
	DB             *sql.DB
	Commands       CommandExecutor
	DispatchHealth func() model.DispatchHealth
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
	case DispatchHealthOperation:
		if d.DispatchHealth == nil {
			err = ErrDispatchHealthUnavailable
		} else {
			result = d.DispatchHealth()
		}
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
	query := "SELECT rowid,id,version,data,updated_at FROM resources WHERE kind=? AND rowid>? AND (?='' OR CASE WHEN kind='operations' THEN json_extract(data,'$.subject') ELSE json_extract(data,'$.project_id') END=?) ORDER BY rowid LIMIT ?"
	rows, err := d.DB.QueryContext(ctx, query, kind, request.EventCursor, request.ProjectID, request.ProjectID, limit+1)
	if err != nil {
		return page, err
	}
	defer func() {
		if closeErr := rows.Close(); resultErr == nil {
			resultErr = closeErr
		}
	}()
	if err := readInspectionPage(rows, &page, limit); err != nil {
		return page, err
	}
	if err := rows.Close(); err != nil {
		return page, err
	}
	if kind == "operations" {
		if err := d.enrichInspectionPage(ctx, &page); err != nil {
			return page, err
		}
	}
	return page, nil
}

func readInspectionPage(rows *sql.Rows, page *RecordPage, limit int) error {
	pageBytes := initialInspectionPageSize
	for rows.Next() {
		record, err := scanInspectionRecord(rows)
		if err != nil {
			return err
		}
		rowBytes, err := inspectionRecordSize(record)
		if err != nil {
			return err
		}
		if len(page.Records) == 0 && rowBytes+inspectionPageHeadroom > maxInspectionPageBytes {
			return errors.New(inspectionRecordTooLarge)
		}
		if len(page.Records) == limit || pageBytes+rowBytes > maxInspectionPageBytes-inspectionPageHeadroom {
			page.HasMore = true
			break
		}
		page.Records = append(page.Records, record)
		page.NextCursor = record.CreatedOrder
		pageBytes += rowBytes
	}
	return rows.Err()
}

func scanInspectionRecord(rows *sql.Rows) (model.Record, error) {
	var record model.Record
	var data []byte
	err := rows.Scan(&record.CreatedOrder, &record.ID, &record.Version, &data, &record.UpdatedAt)
	record.Data = data
	return record, err
}

func inspectionRecordSize(record model.Record) (int, error) {
	encoded, err := json.Marshal(record)
	return len(encoded) + 1, err
}

func (d SupervisorDispatcher) enrichInspectionPage(ctx context.Context, page *RecordPage) error {
	if err := enrichIdeationProgress(ctx, page.Records, func(ctx context.Context, run string) ([]model.IdeationDirectionProgress, error) {
		return store.ReadIdeationProgress(ctx, d.DB, run)
	}); err != nil {
		return err
	}
	// Progress can increase the encoded size after the original page is read.
	return trimEnrichedInspectionPage(page)
}

func trimEnrichedInspectionPage(page *RecordPage) error {
	pageBytes := initialInspectionPageSize
	for index, record := range page.Records {
		rowBytes, err := inspectionRecordSize(record)
		if err != nil {
			return err
		}
		if pageBytes+rowBytes > maxInspectionPageBytes-inspectionPageHeadroom {
			if index == 0 {
				return errors.New(inspectionRecordTooLarge)
			}
			page.Records = page.Records[:index]
			page.NextCursor = page.Records[index-1].CreatedOrder
			page.HasMore = true
			break
		}
		pageBytes += rowBytes
	}
	return nil
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
	pageBytes := initialInspectionPageSize
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
		if rowBytes+inspectionPageHeadroom > maxInspectionPageBytes {
			if len(events) == 0 {
				return nil, errors.New("inspection event exceeds safe local page size")
			}
			break
		}
		if len(events) == limit || pageBytes+rowBytes > maxInspectionPageBytes-inspectionPageHeadroom {
			break
		}
		events = append(events, event)
		pageBytes += rowBytes
	}
	return events, rows.Err()
}

func (d SupervisorDispatcher) snapshot(ctx context.Context, request Request) (Snapshot, error) {
	if d.DB == nil {
		return nil, errors.New(stateDatabaseUnavailable)
	}
	snapshot := Snapshot{}
	for _, kind := range []string{"projects", "execution_plans", "ideas", "operations", "directions", "sessions", "questions", "permissions", "verification", "reviews", "inbox", "plan_versions", "agents", "providers", "project_status", "settings"} {
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
	for _, kind := range []string{"projects", "execution_plans", "ideas", "operations", "directions", "sessions", "questions", "permissions", "verification", "reviews", "inbox", "plan_versions", "agents", "providers", "project_status", "settings"} {
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
