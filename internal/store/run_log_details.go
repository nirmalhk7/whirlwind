package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"whirlwind/internal/model"
)

// A committed mutation remains authoritative if its caller is canceled during
// derived file synchronization. Finish those logs with a bounded independent
// context; cancellation still prevents the transaction itself from committing.
func (s *Store) syncCommittedRunLogs(ctx context.Context, changes []Change) error {
	logCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return s.syncRunLogChanges(logCtx, changes)
}

func (s *Store) recordRunLogChange(ctx context.Context, kind, id string) error {
	if s.runLogs == nil {
		return nil
	}
	ids, err := s.runLogDetailIDs(ctx, kind, id)
	if err != nil {
		return err
	}
	for _, run := range ids {
		body, err := s.runLogDetails(ctx, run)
		if err != nil {
			return err
		}
		if err := s.runLogs.sync(ctx, s, run, body); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) runLogDetailTarget(ctx context.Context, kind, id string) (string, string, error) {
	switch kind {
	case "sessions":
		return id, "", nil
	case "execution_plans":
		return "", id, nil
	case "questions":
		return s.runLogQuestionTarget(ctx, id)
	}
	return "", "", nil
}

func (s *Store) runLogQuestionTarget(ctx context.Context, id string) (string, string, error) {
	record, err := s.Get(ctx, "questions", id)
	if err != nil {
		return "", "", err
	}
	var target struct {
		AttemptID string `json:"attempt_id"`
		TaskID    string `json:"plan_id"`
	}
	if json.Unmarshal(record.Data, &target) != nil {
		return "", "", nil
	}
	return target.AttemptID, target.TaskID, nil
}

func (s *Store) runLogDetailIDs(ctx context.Context, kind, id string) ([]string, error) {
	run, task, err := s.runLogDetailTarget(ctx, kind, id)
	if err != nil {
		return nil, err
	}
	if run != "" {
		return s.existingRunLogID(ctx, run)
	}
	if task == "" {
		return nil, nil
	}
	return s.runLogTaskIDs(ctx, task)
}

func (s *Store) existingRunLogID(ctx context.Context, id string) ([]string, error) {
	_, err := s.Get(ctx, "sessions", id)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return []string{id}, err
}

func (s *Store) runLogTaskIDs(ctx context.Context, task string) (ids []string, resultErr error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id FROM resources WHERE kind='sessions' AND json_extract(data,'$.plan_id')=? ORDER BY rowid", task)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	ids = []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func runLogRunDetails(id string, attempt model.Attempt) map[string]any {
	return map[string]any{
		"id": id, "plan_id": attempt.Task, "provider": attempt.Provider, "provider_name": attempt.ProviderName,
		"role": attempt.Role, "model": attempt.Model, "resolved_model": attempt.ResolvedModel,
		"thinking": attempt.Thinking, "effective_thinking": attempt.EffectiveThinking,
		"status": attempt.Status, "error": attempt.Error, "started_at": attempt.StartedAt,
		"finished_at": attempt.FinishedAt, "finished": attempt.Finished,
	}
}

func (s *Store) runLogDetails(ctx context.Context, id string) ([]byte, error) {
	record, err := s.Get(ctx, "sessions", id)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	attempt, err := Decode[model.Attempt](record)
	if err != nil {
		return nil, err
	}
	return s.runLogDetailsBody(ctx, id, attempt)
}

func (s *Store) runLogDetailsBody(ctx context.Context, id string, attempt model.Attempt) ([]byte, error) {
	questions, err := s.runLogPendingQuestions(ctx, id, attempt.Task)
	if err != nil {
		return nil, err
	}
	meaningful := strings.Join([]string{attempt.Task, attempt.Provider, attempt.ProviderName, attempt.Role, attempt.Model, attempt.ResolvedModel, attempt.Status, attempt.Error, attempt.StartedAt, attempt.FinishedAt}, "")
	if meaningful == "" && len(questions) == 0 {
		return nil, nil
	}
	details := map[string]any{"run": runLogRunDetails(id, attempt), "questions": questions}
	if err := s.addRunLogTask(ctx, details, attempt.Task); err != nil {
		return nil, err
	}
	return SafeJSON(details)
}

func (s *Store) addRunLogTask(ctx context.Context, details map[string]any, id string) error {
	if id == "" {
		return nil
	}
	record, err := s.Get(ctx, "execution_plans", id)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err == nil {
		details["task"] = record.Data
	}
	return err
}

func (s *Store) runLogPendingQuestions(ctx context.Context, run, task string) (questions []model.QuestionSet, resultErr error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT data FROM resources WHERE kind='questions'
AND json_extract(data,'$.status')='pending'
AND (json_extract(data,'$.attempt_id')=? OR
 (coalesce(json_extract(data,'$.attempt_id'),'')='' AND ?<>'' AND json_extract(data,'$.plan_id')=?))
ORDER BY updated_at,id`, run, task, task)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	questions = []model.QuestionSet{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var question model.QuestionSet
		if err := json.Unmarshal(raw, &question); err != nil {
			return nil, err
		}
		question.Answers = nil
		for i := range question.Questions {
			question.Questions[i].Answer = ""
		}
		questions = append(questions, question)
	}
	return questions, rows.Err()
}
