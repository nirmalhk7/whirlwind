package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
	"whirlwind/internal/model"
)

const AttemptPayloadLimit = 25 * 1024 * 1024
const MessagePayloadLimit = 1024 * 1024
const PatchPayloadLimit = 5 * 1024 * 1024

func Prefix(b []byte, limit int) []byte {
	if len(b) <= limit {
		return b
	}
	b = b[:max(0, limit)]
	if len(b) > 0 {
		start := len(b) - 1
		for start > 0 && !utf8.RuneStart(b[start]) {
			start--
		}
		if !utf8.FullRune(b[start:]) {
			b = b[:start]
		}
	}
	return b
}
func (s *Store) Capture(ctx context.Context, attempt, kind, contentType string, content []byte, limit int, replay bool) (model.Artifact, error) {
	return s.CaptureResource(ctx, ArtifactOwner{"sessions", attempt}, kind, contentType, content, CaptureOptions{limit, replay})
}

type ArtifactOwner struct{ Kind, ID string }

type CaptureOptions struct {
	Limit  int
	Replay bool
}

func (s *Store) CaptureResource(ctx context.Context, owner ArtifactOwner, kind, contentType string, content []byte, options CaptureOptions) (model.Artifact, error) {
	content = redactEvidenceContent(contentType, content)

	a := model.Artifact{OwnerKind: owner.Kind, ID: ID(), AttemptID: owner.ID, Kind: kind, ContentType: contentType, Bytes: len(content), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	hash := sha256.Sum256(content)
	a.SHA256 = hex.EncodeToString(hash[:])
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		stream := streamKey(kind, content, options.Replay)
		retainedLimit, err := retainedPayloadLimit(ctx, tx, owner, stream, options.Limit)
		if err != nil {
			return err
		}
		body := Prefix(content, retainedLimit)
		if body == nil {
			body = []byte{}
		}
		a.StoredBytes = len(body)
		a.Truncated = a.StoredBytes < a.Bytes
		_, err = tx.ExecContext(ctx, "INSERT INTO artifacts(id,attempt_id,attempt_kind,kind,content_type,bytes,stored_bytes,sha256,truncated,created_at,content,stream_key) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", a.ID, owner.ID, owner.Kind, kind, contentType, a.Bytes, a.StoredBytes, a.SHA256, a.Truncated, a.CreatedAt, body, stream)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO attempt_events(attempt_id,kind,artifact_id,replay,created_at,metadata) VALUES(?,?,?,?,?,?)", owner.ID, kind, a.ID, options.Replay, a.CreatedAt, string(eventMetadata(content)))
		if err != nil {
			return err
		}
		// Only compact references reach the resumable global event stream.
		data, _ := json.Marshal(a)
		_, err = tx.ExecContext(ctx, "INSERT INTO events(kind,resource_id,data,created_at) VALUES('attempt_events',?,?,?)", owner.ID, string(data), a.CreatedAt)
		return err
	})
	return a, err
}

func redactEvidenceContent(contentType string, content []byte) []byte {
	if strings.HasPrefix(strings.ToLower(contentType), "application/json") {
		var value any
		if json.Unmarshal(content, &value) == nil {
			if redacted, err := SafeJSON(value); err == nil {
				return redacted
			}
		}
	}
	if utf8.Valid(content) && (strings.HasPrefix(strings.ToLower(contentType), "text/") || contentType == "") {
		return []byte(RedactText(string(content)))
	}
	return content
}

func retainedPayloadLimit(ctx context.Context, tx *sql.Tx, owner ArtifactOwner, stream string, limit int) (int, error) {
	var used int
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(sum(stored_bytes),0) FROM artifacts WHERE attempt_id=? AND attempt_kind=?", owner.ID, owner.Kind).Scan(&used); err != nil {
		return 0, err
	}
	if stream != "" {
		var streamUsed int
		if err := tx.QueryRowContext(ctx, "SELECT COALESCE(sum(stored_bytes),0) FROM artifacts WHERE attempt_id=? AND stream_key=?", owner.ID, stream).Scan(&streamUsed); err != nil {
			return 0, err
		}
		limit = min(limit, max(0, MessagePayloadLimit-streamUsed))
	}
	return min(limit, max(0, AttemptPayloadLimit-used)), nil
}
func (s *Store) ArtifactMetadata(ctx context.Context, id string) (model.Artifact, error) {
	var a model.Artifact
	err := s.DB.QueryRowContext(ctx, "SELECT id,attempt_id,attempt_kind,kind,content_type,bytes,stored_bytes,sha256,truncated,created_at FROM artifacts WHERE id=?", id).Scan(&a.ID, &a.AttemptID, &a.OwnerKind, &a.Kind, &a.ContentType, &a.Bytes, &a.StoredBytes, &a.SHA256, &a.Truncated, &a.CreatedAt)
	if err == sql.ErrNoRows {
		err = ErrNotFound
	}
	return a, err
}
func (s *Store) Artifact(ctx context.Context, id string) (model.Artifact, []byte, error) {
	a, err := s.ArtifactMetadata(ctx, id)
	if err != nil {
		return a, nil, err
	}
	var body []byte
	err = s.DB.QueryRowContext(ctx, "SELECT content FROM artifacts WHERE id=?", id).Scan(&body)
	if err == sql.ErrNoRows {
		err = ErrNotFound
	}
	return a, body, err
}

func (s *Store) AttemptEvents(ctx context.Context, attempt string, after int64, limit int) ([]model.AttemptEvent, error) {
	if _, err := s.Get(ctx, "sessions", attempt); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT sequence,kind,artifact_id,replay,created_at,metadata FROM attempt_events WHERE attempt_id=? AND sequence>? ORDER BY sequence LIMIT ?", attempt, after, limit)
	if err != nil {
		return nil, err
	}
	events, err := readAttemptEvents(rows, attempt)
	if err = errors.Join(err, rows.Close()); err != nil {
		return nil, err
	}
	for i := range events {
		a, err := s.ArtifactMetadata(ctx, events[i].Artifact.ID)
		if err != nil {
			return nil, err
		}
		events[i].Artifact = &a
	}
	return events, nil
}

// AttemptActionEvents returns only durable tool action boundary metadata used
// to decide whether an interrupted attempt can be safely retried.
func (s *Store) AttemptActionEvents(ctx context.Context, attempt string) ([]model.AttemptEvent, error) {
	if _, err := s.Get(ctx, "sessions", attempt); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT sequence,kind,created_at,metadata FROM attempt_events
		WHERE attempt_id=? AND kind IN ('whirlwind/action_started','whirlwind/action_result','whirlwind/terminal/exit')
		ORDER BY sequence`, attempt)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	events := []model.AttemptEvent{}
	for rows.Next() {
		var event model.AttemptEvent
		var metadata string
		event.AttemptID = attempt
		if err := rows.Scan(&event.Sequence, &event.Kind, &event.CreatedAt, &metadata); err != nil {
			return nil, err
		}
		event.Metadata = json.RawMessage(metadata)
		events = append(events, event)
	}
	return events, rows.Err()
}

func readAttemptEvents(rows *sql.Rows, attempt string) ([]model.AttemptEvent, error) {
	events := []model.AttemptEvent{}
	for rows.Next() {
		var e model.AttemptEvent
		var id, meta string
		e.AttemptID = attempt
		if err := rows.Scan(&e.Sequence, &e.Kind, &id, &e.Replay, &e.CreatedAt, &meta); err != nil {
			return nil, err
		}
		e.Metadata = json.RawMessage(meta)
		e.Artifact = &model.Artifact{ID: id}
		events = append(events, e)
	}
	return events, rows.Err()
}
