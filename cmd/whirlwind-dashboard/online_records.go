package main

import (
	"context"
	"encoding/json"
	"fmt"

	"whirlwind/internal/control"
)

func completeOnlineRecords(ctx context.Context, client *control.Client, snapshot control.Snapshot, kind string) (bool, error) {
	started := false
	var cursor int64
	for {
		request := control.Request{RequestID: fmt.Sprintf("dashboard-records-%s-%d", kind, cursor), Operation: "inspect.records", ResourceID: kind, EventCursor: cursor, Limit: 100}
		response, err := dashboardCall(ctx, client, request)
		if err != nil {
			if legacySettingsUnavailable(kind, cursor, err) {
				return started, nil
			}
			return started, err
		}
		started = true
		page, err := decodeOnlineRecordPage(response.Result, kind, cursor)
		if err != nil {
			return started, err
		}
		snapshot[kind] = append(snapshot[kind], page.Records...)
		if !page.HasMore {
			return started, nil
		}
		cursor = page.NextCursor
	}
}

func decodeOnlineRecordPage(result any, kind string, cursor int64) (control.RecordPage, error) {
	var page control.RecordPage
	encoded, err := json.Marshal(result)
	if err != nil {
		return page, err
	}
	if err := json.Unmarshal(encoded, &page); err != nil {
		return page, fmt.Errorf("decode inspection page for %s: %w", kind, err)
	}
	return page, validateOnlineRecordPage(page, kind, cursor)
}

func validateOnlineRecordPage(page control.RecordPage, kind string, cursor int64) error {
	if page.Kind != kind || page.Cursor != cursor {
		return fmt.Errorf("inspection page authority changed for %s", kind)
	}
	if len(page.Records) == 0 {
		if page.HasMore {
			return fmt.Errorf("inspection cursor did not advance for %s", kind)
		}
	} else if page.NextCursor <= cursor || page.Records[len(page.Records)-1].CreatedOrder != page.NextCursor {
		return fmt.Errorf("inspection cursor did not advance for %s", kind)
	}
	return nil
}
