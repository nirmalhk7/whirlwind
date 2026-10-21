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
	return s.queryLessons(ctx, project, role, status, after, limit, "")
}

func (s *Store) queryLessons(ctx context.Context, project, role, status string, after int64, limit int, query string) (LessonPage, error) {
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
	if query != "" {
		statement += " AND json_extract(data,'$.status') IN ('tentative','corroborated') AND ("
		// Structured matches see the complete query, even for very long issues.
		pathMatch := `EXISTS(SELECT 1 FROM json_each(data,'$.paths') WHERE length(value)>0 AND instr(lower(?),lower(value))>0)`
		symbolMatch := `EXISTS(SELECT 1 FROM json_each(data,'$.symbols') WHERE length(value)>0 AND instr(lower(?),lower(value))>0)`
		errorMatch := `length(COALESCE(json_extract(data,'$.error_signature'),''))>0 AND instr(lower(?),lower(json_extract(data,'$.error_signature')))>0`
		args = append(args, query, query, query)
		terms := strings.Fields(strings.ToLower(query))
		seen := map[string]bool{}
		clauses := []string{pathMatch, symbolMatch, "(" + errorMatch + ")"}
		ranks := []string{"CASE WHEN " + pathMatch + " THEN 2000 ELSE 0 END", "CASE WHEN " + symbolMatch + " THEN 1000 ELSE 0 END", "CASE WHEN " + errorMatch + " THEN 3000 ELSE 0 END"}
		rankArgs := []any{query, query, query}
		for _, term := range terms {
			if len(term) < 3 || seen[term] {
				continue
			}
			seen[term] = true
			clauses = append(clauses, `lower(data) LIKE ? ESCAPE '\'`)
			term = strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(term)
			args = append(args, "%"+term+"%")
			ranks = append(ranks, `CASE WHEN lower(COALESCE(json_extract(data,'$.paths'),'') || COALESCE(json_extract(data,'$.symbols'),'') || COALESCE(json_extract(data,'$.error_signature'),'')) LIKE ? ESCAPE '\' THEN 20 ELSE 0 END`)
			rankArgs = append(rankArgs, "%"+term+"%")
			if len(seen) == 32 {
				break
			}
		}
		statement += strings.Join(clauses, " OR ") + ")"
		order = "(" + strings.Join(ranks, " + ") + ") DESC,updated_at DESC,rowid DESC"
		args = append(args, rankArgs...)
	}
	statement += " ORDER BY " + order + " LIMIT ?"
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, statement, args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
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

func (s *Store) MatchingLessons(ctx context.Context, project, role, query string) ([]model.Record, error) {
	page, err := s.queryLessons(ctx, project, role, "", 0, 128, query)
	return page.Items, err
}

// CommitLearning applies reports once, with lesson changes and audit events in one transaction.
func (s *Store) CommitLearning(ctx context.Context, attempt model.Attempt, report model.LearningReport) (*model.LearningSnapshot, error) {
	snapshot := model.LearningSnapshot{Recalled: []model.LessonRef{}}
	if attempt.Learning != nil {
		snapshot = *attempt.Learning
		snapshot.Warnings = append([]string{}, attempt.Learning.Warnings...)
	}
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		old, err := lessonRecord(ctx, tx, "learning_reports", attempt.AttemptID)
		if err == nil {
			return json.Unmarshal(old.Data, &snapshot)
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		for _, candidate := range report.Lessons {
			candidate.ProjectID = attempt.ProjectID
			candidate.Role = attempt.Role
			candidate.AttemptID = attempt.AttemptID
			candidate.TaskID = attempt.Task
			candidate.Provider = attempt.Provider
			candidate.Model = attempt.Model
			candidate.Status = "tentative"
			if err := checkLessonEvidence(ctx, tx, attempt, candidate.EvidenceIDs, false); err != nil {
				snapshot.Warnings = append(snapshot.Warnings, "Lesson evidence rejected")
				continue
			}
			candidate.ID = lessonID(candidate)
			previous, err := lessonRecord(ctx, tx, "lessons", candidate.ID)
			if err == nil {
				var saved model.Lesson
				if err = json.Unmarshal(previous.Data, &saved); err != nil {
					return err
				}
				// Repetition is not independent confirmation. Retired advice never revives implicitly.
				if saved.Status != "stale" {
					continue
				}
				if err = put(ctx, tx, "lessons", candidate.ID, candidate, previous.Version); err != nil {
					return err
				}
			} else if errors.Is(err, ErrNotFound) {
				if err = put(ctx, tx, "lessons", candidate.ID, candidate, 0); err != nil {
					return err
				}
			} else {
				return err
			}
			snapshot.CreatedIDs = append(snapshot.CreatedIDs, candidate.ID)
			for _, id := range candidate.EvidenceIDs {
				if err = recordLessonEvidence(ctx, tx, candidate.ID, id); err != nil {
					return err
				}
			}
			if err = lessonAudit(ctx, tx, attempt, "lesson_recorded", candidate.ID, ""); err != nil {
				return err
			}
		}
		for _, feedback := range report.Feedback {
			var ref *model.LessonRef
			for i := range snapshot.Recalled {
				if snapshot.Recalled[i].ID == feedback.LessonID {
					ref = &snapshot.Recalled[i]
					break
				}
			}
			if ref == nil {
				snapshot.Warnings = append(snapshot.Warnings, "Feedback rejected: lesson was not recalled")
				continue
			}
			r, err := lessonRecord(ctx, tx, "lessons", feedback.LessonID)
			if err != nil {
				return err
			}
			var l model.Lesson
			if err = json.Unmarshal(r.Data, &l); err != nil {
				return err
			}
			if l.ProjectID != attempt.ProjectID || l.Role != attempt.Role || l.Status == "retired" || r.Version != ref.Version {
				snapshot.Warnings = append(snapshot.Warnings, "Feedback rejected: scope or version changed")
				continue
			}
			reused := false
			for _, id := range feedback.EvidenceIDs {
				if slices.Contains(l.EvidenceIDs, id) {
					reused = true
				}
				if _, lookupErr := lessonRecord(ctx, tx, "lesson_evidence", lessonEvidenceID(l.ID, id)); lookupErr == nil {
					reused = true
				} else if !errors.Is(lookupErr, ErrNotFound) {
					return lookupErr
				}
			}
			if err = checkLessonEvidence(ctx, tx, attempt, feedback.EvidenceIDs, true); err != nil || reused {
				snapshot.Warnings = append(snapshot.Warnings, "Feedback rejected: additional evidence required")
				continue
			}
			switch feedback.Verdict {
			case "supports":
				l.Status = "corroborated"
				if l.Anchors == nil {
					l.Anchors = map[string]string{}
				}
				for path, hash := range feedback.Anchors {
					l.Anchors[path] = hash
				}
			case "contradicts", "supersedes":
				l.Status = "retired"
			default:
				snapshot.Warnings = append(snapshot.Warnings, "Feedback rejected: unknown verdict")
				continue
			}
			l.Reason = feedback.Reason
			l.EvidenceIDs = append(l.EvidenceIDs, feedback.EvidenceIDs...)
			// Retain bounded recent evidence; the audit preserves older references.
			seen := map[string]bool{}
			recent := []string{}
			for i := len(l.EvidenceIDs) - 1; i >= 0 && len(recent) < 8; i-- {
				id := l.EvidenceIDs[i]
				if !seen[id] {
					recent = append(recent, id)
					seen[id] = true
				}
			}
			slices.Reverse(recent)
			l.EvidenceIDs = recent
			if err = put(ctx, tx, "lessons", l.ID, l, r.Version); err != nil {
				return err
			}
			for _, id := range feedback.EvidenceIDs {
				if err = recordLessonEvidence(ctx, tx, l.ID, id); err != nil {
					return err
				}
			}
			if err = lessonAudit(ctx, tx, attempt, "lesson_"+feedback.Verdict, l.ID, feedback.Reason); err != nil {
				return err
			}
			snapshot.Feedback = append(snapshot.Feedback, feedback)
		}
		return put(ctx, tx, "learning_reports", attempt.AttemptID, snapshot, 0)
	})
	return &snapshot, err
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

func checkLessonEvidence(ctx context.Context, tx *sql.Tx, a model.Attempt, ids []string, independent bool) error {
	if independent && len(ids) == 0 {
		return errors.New("additional evidence required")
	}
	for _, id := range ids {
		var kind, ownerKind string
		var truncated bool
		var retained int
		var data []byte
		err := tx.QueryRowContext(ctx, `SELECT a.kind,a.attempt_kind,r.data,a.truncated,a.stored_bytes FROM artifacts a JOIN resources r ON r.kind=a.attempt_kind AND r.id=a.attempt_id WHERE a.id=?`, id).Scan(&kind, &ownerKind, &data, &truncated, &retained)
		if err != nil {
			return err
		}
		if truncated || retained == 0 {
			return errors.New("complete retained evidence required")
		}
		var scope struct {
			Project string `json:"project_id"`
			Task    string `json:"task"`
		}
		if err = json.Unmarshal(data, &scope); err != nil {
			return err
		}
		if scope.Task != a.Task {
			return errors.New("evidence outside task")
		}
		if ownerKind == "sessions" && scope.Project != a.ProjectID {
			return errors.New("evidence outside project")
		}
		if ownerKind == "verification" {
			task, err := lessonRecord(ctx, tx, "tasks", scope.Task)
			if err != nil {
				return err
			}
			var t model.Task
			if err = json.Unmarshal(task.Data, &t); err != nil {
				return err
			}
			if t.ProjectID != a.ProjectID {
				return errors.New("evidence outside project")
			}
		}
		if independent && !strings.HasPrefix(kind, "check:") && kind != "learning_source" {
			return errors.New("observed source or check evidence required")
		}
	}
	return nil
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
	if r.Version != expected {
		return r, ErrConflict
	}
	var l model.Lesson
	if err = json.Unmarshal(r.Data, &l); err != nil {
		return r, err
	}
	if l.Status == "retired" {
		return r, nil
	}
	l.Status = "retired"
	l.Reason = "Retired by operator"
	if err = s.Put(ctx, "lessons", id, l, expected); err != nil {
		return r, err
	}
	return s.Get(ctx, "lessons", id)
}
