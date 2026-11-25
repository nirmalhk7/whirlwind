package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

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
