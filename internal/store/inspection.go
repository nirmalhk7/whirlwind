package store

import (
	"context"
	"encoding/json"
	"whirlwind/internal/model"
)

type EvidenceRecord struct {
	Kind   string       `json:"kind"`
	Record model.Record `json:"record"`
}

// LinkedEvidence includes reviewed checks even when their originating attempt differs from the reviewer.
func (s *Store) LinkedEvidence(ctx context.Context, attempt string) ([]EvidenceRecord, error) {
	if _, err := s.Get(ctx, "sessions", attempt); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT r.kind,r.id,r.version,r.data,r.updated_at FROM resources r
 WHERE r.kind IN ('verification','reviews','review_parts','checkpoints','questions','permissions','audit','quota_observations')
 AND (json_extract(r.data,'$.attempt_id')=? OR EXISTS(SELECT 1 FROM json_each(r.data,'$.provenance.attempt_ids') WHERE value=?)
 OR (r.kind='verification' AND EXISTS(SELECT 1 FROM resources assessment
 WHERE assessment.kind IN ('reviews','review_parts') AND json_extract(assessment.data,'$.provenance.verification_id')=r.id
 AND EXISTS(SELECT 1 FROM json_each(assessment.data,'$.provenance.attempt_ids') WHERE value=?))))
 ORDER BY r.updated_at DESC,r.kind,r.id LIMIT 100`, attempt, attempt, attempt)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	records := []EvidenceRecord{}
	for rows.Next() {
		var item EvidenceRecord
		var data []byte
		if err = rows.Scan(&item.Kind, &item.Record.ID, &item.Record.Version, &data, &item.Record.UpdatedAt); err != nil {
			return nil, err
		}
		item.Record.Data = json.RawMessage(data)
		records = append(records, item)
	}
	return records, rows.Err()
}
