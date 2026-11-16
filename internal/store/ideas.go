package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"whirlwind/internal/model"
)

func ideaContent(i model.Idea) string {
	content, _ := json.Marshal([]any{i.What, i.Why, i.Where, i.HowItHelps, i.Mechanism, i.Components, i.Evidence, i.RepositoryRevision})
	return string(content)
}

func prepareIdea(id string, before, data []byte) ([]byte, error) {
	var idea model.Idea
	if err := json.Unmarshal(data, &idea); err != nil {
		return nil, err
	}
	if idea.ID != id || idea.ProjectID == "" || idea.RunID == "" {
		return nil, fmt.Errorf("%w: idea identity is required", ErrConflict)
	}
	for _, field := range []string{idea.Title, idea.What, idea.Why, idea.Where, idea.HowItHelps} {
		if strings.TrimSpace(field) == "" {
			return nil, fmt.Errorf("%w: complete idea content is required", ErrConflict)
		}
	}
	if len(before) > 0 {
		var old model.Idea
		if err := json.Unmarshal(before, &old); err != nil {
			return nil, err
		}
		if !ideaRevisionAllowed(old, idea) {
			return nil, ErrConflict
		}
	} else if idea.DecisionStatus != "draft" {
		return nil, ErrConflict
	}
	if idea.NoveltyMethod != "legacy_llm" && idea.NoveltyMethod != "not_checked" {
		return nil, fmt.Errorf("%w: unsupported novelty method", ErrConflict)
	}
	fingerprint := sha256.Sum256([]byte(ideaContent(idea)))
	idea.Fingerprint = hex.EncodeToString(fingerprint[:])
	return json.Marshal(idea)
}

func ideaTransition(from, to string) bool {
	if from == to {
		return true
	}
	switch from {
	case "draft":
		return to == "approved"
	case "approved":
		return to == "in_process"
	case "in_process":
		return to == "complete" || to == "error"
	case "error":
		return to == "approved"
	}
	return false
}

func syncIdeaTx(ctx context.Context, tx *sql.Tx, id string, data []byte) error {
	var i model.Idea
	if err := json.Unmarshal(data, &i); err != nil {
		return err
	}
	if i.PlanID != "" {
		var linkedIdea string
		if err := tx.QueryRowContext(ctx, "SELECT idea_id FROM plans WHERE plan_id=?", i.PlanID).Scan(&linkedIdea); err != nil {
			if err == sql.ErrNoRows {
				return ErrConflict
			}
			return err
		}
		if linkedIdea != i.ID {
			return ErrConflict
		}
	}
	var at string
	if err := tx.QueryRowContext(ctx, "SELECT updated_at FROM resources WHERE kind='ideas' AND id=?", id).Scan(&at); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO ideas(
	 idea_id,project_id,run_id,idea_title,idea_type,what,why,"where",how,status,timestamp)
	 VALUES(?,?,?,?,?,?,?,?,?,?,?)
	 ON CONFLICT(idea_id) DO UPDATE SET idea_title=excluded.idea_title,
	 what=excluded.what,why=excluded.why,"where"=excluded."where",how=excluded.how,status=excluded.status`,
		i.ID, i.ProjectID, i.RunID, i.Title, i.Direction, i.What, i.Why, i.Where, i.HowItHelps, i.DecisionStatus, at)
	if err != nil {
		return err
	}
	return syncIdeaHistoryTx(ctx, tx, i, at)
}

func syncIdeaHistoryTx(ctx context.Context, tx *sql.Tx, i model.Idea, at string) error {
	status := i.DecisionStatus
	if i.ArchivedAt != "" {
		status = "archived"
	}
	d := model.Direction{IdeaFields: &model.IdeaDraft{What: i.What, Why: i.Why, Where: i.Where, HowItHelps: i.HowItHelps}, ID: i.ID, ProjectID: i.ProjectID, Category: i.Direction, Title: i.Title, Description: i.HowItHelps, Problem: i.Why, Mechanism: i.Mechanism, Components: i.Components, Evidence: i.Evidence, RepositoryRevision: i.RepositoryRevision, Status: status, RejectionReason: i.ArchiveReason, PredecessorID: i.PredecessorID, RevisitReason: i.RevisitReason}
	if i.PlanID != "" {
		d.ApprovedPlanIDs = []string{i.PlanID}
	}
	var previous []byte
	err := tx.QueryRowContext(ctx, "SELECT data FROM proposals WHERE project_id=? AND id=?", i.ProjectID, i.ID).Scan(&previous)
	if err == sql.ErrNoRows {
		if err := syncProposalTx(ctx, tx, d); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		if err := updateIdeaHistoryTx(tx, previous, d, at); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, "UPDATE proposals_fts SET terms=terms || char(10) || ? || char(10) || ? WHERE project_id=? AND proposal_id=?", i.What, i.Where, i.ProjectID, i.ID)
	return err
}

func ideaRevisionAllowed(old, idea model.Idea) bool {
	if old.ProjectID != idea.ProjectID || old.RunID != idea.RunID || old.Direction != idea.Direction || old.PredecessorID != idea.PredecessorID || old.RevisitReason != idea.RevisitReason ||
		((old.DecisionStatus != "draft" || old.ArchivedAt != "") && (old.Title != idea.Title || ideaContent(old) != ideaContent(idea))) ||
		(old.ArchivedAt != "" && (idea.ArchivedAt != old.ArchivedAt || idea.DecisionStatus != old.DecisionStatus)) ||
		!ideaTransition(old.DecisionStatus, idea.DecisionStatus) {
		return false
	}
	return true
}

func updateIdeaHistoryTx(tx *sql.Tx, previous []byte, d model.Direction, at string) error {
	var old model.Proposal
	if err := json.Unmarshal(previous, &old); err != nil {
		return err
	}
	p := proposalFromDirection(d, at)
	p.CreatedAt = old.CreatedAt
	if p.RejectionReason == "" {
		p.RejectionReason = old.RejectionReason
	}
	// The canonical idea already owns its human reconsideration identity;
	// an authorized draft edit must not consume that authorization again.
	if err := writeProposalTx(tx, p, false); err != nil {
		return err
	}
	if err := saveProposalFingerprintTx(tx, p, false); err != nil {
		return err
	}
	return nil
}
