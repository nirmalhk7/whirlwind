package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"whirlwind/internal/model"
)

// ReserveIdeaCandidate records an allowance before generation. A process
// restart reuses the unfinished logical candidate rather than spending another.
// The caller must own the project's generation lease while invoking the model.
func (s *Store) ReserveIdeaCandidate(ctx context.Context, runID, direction string) (id string, ordinal int, err error) {
	err = s.Transaction(ctx, func(tx *sql.Tx) error {
		var state string
		if err := tx.QueryRowContext(ctx, "SELECT candidate_count,status FROM ideation_directions WHERE run_id=? AND direction=?", runID, direction).Scan(&ordinal, &state); err != nil {
			return err
		}
		if state == "completed" {
			return ErrConflict
		}
		err := tx.QueryRowContext(ctx, "SELECT candidate_id,ordinal FROM idea_candidates WHERE run_id=? AND direction=? AND status IN ('generating','generated') ORDER BY ordinal DESC LIMIT 1", runID, direction).Scan(&id, &ordinal)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if ordinal >= 5 {
			return ErrConflict
		}
		ordinal++
		id = model.NewIdeaID()
		if _, err := tx.ExecContext(ctx, "INSERT INTO idea_candidates(candidate_id,run_id,direction,ordinal,status) VALUES(?,?,?,?,'generating')", id, runID, direction, ordinal); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE ideation_directions SET candidate_count=?,status='running' WHERE run_id=? AND direction=?", ordinal, runID, direction)
		return err
	})
	return
}

func (s *Store) GeneratedIdeaCandidate(ctx context.Context, id string) (model.Direction, bool, error) {
	var status, content string
	if err := s.DB.QueryRowContext(ctx, "SELECT status,content FROM idea_candidates WHERE candidate_id=?", id).Scan(&status, &content); err != nil {
		return model.Direction{}, false, err
	}
	if status != "generated" {
		return model.Direction{}, false, nil
	}
	var candidate model.Direction
	err := json.Unmarshal([]byte(content), &candidate)
	return candidate, true, err
}

// CompleteIdeaCandidate commits one direction immediately. A later direction's
// model failure cannot roll back this accepted idea. A nil idea records a
// completed rejection, allowing the next bounded candidate reservation.
func (s *Store) CompleteIdeaCandidate(ctx context.Context, id string, idea *model.Idea, reason string) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		return completeIdeaCandidateTx(ctx, tx, id, idea, reason)
	})
}

// RecordGeneratedIdeaCandidates freezes model output before content validation. The
// candidate allowance already exists; no model-provided identity creates one.
func (s *Store) RecordGeneratedIdeaCandidates(ctx context.Context, candidates map[string]model.Direction) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		for id, candidate := range candidates {
			if err := recordGeneratedIdeaCandidateTx(ctx, tx, id, candidate); err != nil {
				return err
			}
		}
		return nil
	})
}

func completeIdeaCandidateTx(ctx context.Context, tx *sql.Tx, id string, idea *model.Idea, reason string) error {
	var run, direction, state string
	if err := tx.QueryRowContext(ctx, "SELECT run_id,direction,status FROM idea_candidates WHERE candidate_id=?", id).Scan(&run, &direction, &state); err != nil {
		return err
	}
	if state != "generating" && state != "generated" {
		return ErrConflict
	}
	var runStatus string
	if err := tx.QueryRowContext(ctx, "SELECT status FROM ideation_runs WHERE run_id=?", run).Scan(&runStatus); err != nil {
		return err
	}
	if runStatus != "running" && runStatus != "queued" {
		return ErrConflict
	}
	if idea == nil {
		if reason == "" {
			return ErrConflict
		}
		_, err := tx.ExecContext(ctx, "UPDATE idea_candidates SET status='rejected',reason=? WHERE candidate_id=?", reason, id)
		return err
	}
	if idea.ID != id || idea.RunID != run || idea.Direction != direction {
		return ErrConflict
	}
	if err := put(ctx, tx, "ideas", idea.ID, *idea, 0); err != nil {
		return err
	}
	var data []byte
	if err := tx.QueryRowContext(ctx, "SELECT data FROM resources WHERE kind='ideas' AND id=?", idea.ID).Scan(&data); err != nil {
		return err
	}
	var saved model.Idea
	if err := json.Unmarshal(data, &saved); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE idea_candidates SET status='accepted',fingerprint=?,content=? WHERE candidate_id=?", saved.Fingerprint, string(data), id); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "UPDATE ideation_directions SET status='completed',idea_id=?,error_reason='' WHERE run_id=? AND direction=?", idea.ID, run, direction)
	return err
}

func recordGeneratedIdeaCandidateTx(ctx context.Context, tx *sql.Tx, id string, candidate model.Direction) error {
	var direction, status, runStatus string
	if err := tx.QueryRowContext(ctx, "SELECT c.direction,c.status,r.status FROM idea_candidates c JOIN ideation_runs r ON r.run_id=c.run_id WHERE c.candidate_id=?", id).Scan(&direction, &status, &runStatus); err != nil {
		return err
	}
	if candidate.ID != id || candidate.Category != direction || status != "generating" || (runStatus != "running" && runStatus != "queued") {
		return ErrConflict
	}
	raw, err := json.Marshal(candidate)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE idea_candidates SET status='generated',fingerprint=?,content=? WHERE candidate_id=?", model.Fingerprint(raw), string(raw), id); err != nil {
		return err
	}
	return nil
}
