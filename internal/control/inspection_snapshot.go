package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"whirlwind/internal/model"
)

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
