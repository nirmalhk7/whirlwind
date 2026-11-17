package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const (
	inspectOperationPrefix   = "inspect."
	stateDatabaseUnavailable = "supervisor state database unavailable"
	maxInspectionPageBytes   = 512 << 10
)

var inspectableKinds = map[string]bool{
	"projects": true, "execution_plans": true, "ideas": true, "operations": true, "directions": true, "sessions": true,
	"questions": true, "permissions": true, "verification": true,
	"reviews": true, "inbox": true, "plan_versions": true, "artifacts": true, "agents": true, "providers": true, "project_status": true, "settings": true,
}

type RecordPage struct {
	Kind       string         `json:"kind"`
	ProjectID  string         `json:"project_id,omitempty"`
	Cursor     int64          `json:"cursor"`
	NextCursor int64          `json:"next_cursor"`
	HasMore    bool           `json:"has_more"`
	Records    []model.Record `json:"records"`
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
	if err := rows.Close(); err != nil {
		return page, err
	}
	if kind == "operations" {
		if err := enrichIdeationProgress(ctx, page.Records, func(ctx context.Context, run string) ([]model.IdeationDirectionProgress, error) {
			return store.ReadIdeationProgress(ctx, d.DB, run)
		}); err != nil {
			return page, err
		}
		pageBytes = 128
		for n, record := range page.Records {
			encoded, err := json.Marshal(record)
			if err != nil {
				return page, err
			}
			if pageBytes+len(encoded)+1 > maxInspectionPageBytes-1024 {
				if n == 0 {
					return page, errors.New("inspection record exceeds safe local page size")
				}
				page.Records = page.Records[:n]
				page.NextCursor = page.Records[n-1].CreatedOrder
				page.HasMore = true
				break
			}
			pageBytes += len(encoded) + 1
		}
	}
	return page, nil
}
