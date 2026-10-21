package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"whirlwind/internal/model"
)

type LessonPage struct {
	Items     []model.Record `json:"items"`
	NextAfter int64          `json:"next_after"`
}

// LessonsPage uses stable creation order rather than mutable timestamps.
func (s *Store) LessonsPage(ctx context.Context, project, role, status string, after int64, limit int) (LessonPage, error) {
	return s.queryLessons(ctx, project, role, status, after, limit)
}

func (s *Store) queryLessons(ctx context.Context, project, role, status string, after int64, limit int) (LessonPage, error) {
	page := LessonPage{Items: []model.Record{}, NextAfter: after}
	statement := "SELECT rowid,id,version,data,updated_at FROM resources WHERE kind='lessons' AND json_extract(data,'$.project_id')=? AND rowid>?"
	args := []any{project, after}
	order := "rowid"
	if role != "" {
		statement += " AND json_extract(data,'$.role')=?"
		args = append(args, role)
	}
	if status != "" {
		statement += " AND json_extract(data,'$.status')=?"
		args = append(args, status)
	}

	statement += " ORDER BY " + order + " LIMIT ?"
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, statement, args...)
	if err != nil {
		return page, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var r model.Record
		var b []byte
		if err = rows.Scan(&r.CreatedOrder, &r.ID, &r.Version, &b, &r.UpdatedAt); err != nil {
			return page, err
		}
		r.Data = json.RawMessage(b)
		page.Items = append(page.Items, r)
		page.NextAfter = r.CreatedOrder
	}
	return page, rows.Err()
}

func lessonEvidenceID(lesson, artifact string) string {
	sum := sha256.Sum256([]byte(lesson + ":" + artifact))
	return hex.EncodeToString(sum[:])
}

func recordLessonEvidence(ctx context.Context, tx *sql.Tx, lesson, artifact string) error {
	id := lessonEvidenceID(lesson, artifact)
	if _, err := lessonRecord(ctx, tx, "lesson_evidence", id); err == nil {
		return nil
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	return put(ctx, tx, "lesson_evidence", id, map[string]string{"lesson_id": lesson, "artifact_id": artifact}, 0)
}

func lessonID(l model.Lesson) string {
	paths := append([]string{}, l.Paths...)
	slices.Sort(paths)
	symbols := append([]string{}, l.Symbols...)
	slices.Sort(symbols)
	parts := []string{l.ProjectID, l.Role, l.Trigger, l.Mistake, l.Correction, strings.Join(paths, "|"), strings.Join(symbols, "|"), l.ErrorSignature}
	for i := range parts {
		parts[i] = strings.Join(strings.Fields(parts[i]), " ")
	}
	b, _ := json.Marshal(parts)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}

func lessonRecord(ctx context.Context, tx *sql.Tx, kind, id string) (model.Record, error) {
	var r model.Record
	r.ID = id
	var b []byte
	err := tx.QueryRowContext(ctx, "SELECT version,data FROM resources WHERE kind=? AND id=?", kind, id).Scan(&r.Version, &b)
	r.Data = json.RawMessage(b)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return r, err
}

func lessonAudit(ctx context.Context, tx *sql.Tx, a model.Attempt, action, id, reason string) error {
	return put(ctx, tx, "audit", ID(), model.Audit{Task: a.Task, AttemptID: a.AttemptID, Source: "agent_learning", Action: action, Target: id, Reason: reason, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}, 0)
}

func (s *Store) MarkLessonStale(ctx context.Context, r model.Record, l model.Lesson) error {
	l.Status = "stale"
	l.Reason = "Supporting source changed or is unavailable"
	return s.Write(ctx, []Change{{Kind: "lessons", ID: l.ID, Value: l, Expected: r.Version}, {Kind: "audit", ID: ID(), Value: model.Audit{Source: "agent_learning", Action: "lesson_stale", Target: l.ID, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}}}, nil)
}

func (s *Store) RetireLesson(ctx context.Context, id string, expected int) (model.Record, error) {
	r, err := s.Get(ctx, "lessons", id)
	if err != nil {
		return r, err
	}
	lesson, err := retiringLesson(r, expected)
	if err != nil || lesson.Status == "retired" {
		return r, err
	}
	lesson.Status = "retired"
	lesson.Reason = "Retired by operator"
	if err = s.Put(ctx, "lessons", id, lesson, expected); err != nil {
		return r, err
	}
	return s.Get(ctx, "lessons", id)
}

func retiringLesson(record model.Record, expected int) (model.Lesson, error) {
	var lesson model.Lesson
	if record.Version != expected {
		return lesson, ErrConflict
	}
	err := json.Unmarshal(record.Data, &lesson)
	return lesson, err
}
