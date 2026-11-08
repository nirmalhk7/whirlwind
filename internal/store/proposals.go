package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"whirlwind/internal/model"
)

// RecordProposalRejection preserves rejected proposals and their reasons outside active directions.
func (s *Store) RecordProposalRejection(ctx context.Context, proposal model.Proposal, reason string) error {
	if proposal.ID == "" || proposal.ProjectID == "" || strings.TrimSpace(reason) == "" {
		return errors.New("rejected proposal ID, project, and reason are required")
	}
	proposal.Status, proposal.RejectionReason = "rejected", strings.TrimSpace(reason)
	if proposal.CreatedAt == "" {
		proposal.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		var fingerprintOwner string
		err := tx.QueryRowContext(ctx, "SELECT proposal_id FROM proposal_fingerprints WHERE project_id=? AND fingerprint=?", proposal.ProjectID, proposal.Fingerprint).Scan(&fingerprintOwner)
		if errors.Is(err, sql.ErrNoRows) {
			return writeProposalTx(tx, proposal, true)
		}
		if err != nil {
			return err
		}
		if err := writeProposalTx(tx, proposal, false); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE proposal_fingerprints SET reason=?,kind='rejected',created_at=? WHERE project_id=? AND fingerprint=?", proposal.RejectionReason, proposal.CreatedAt, proposal.ProjectID, proposal.Fingerprint)
		return err
	})
}

// UpdateProposalOutcome keeps dismissed, deferred, and implemented history after UI removal.
func (s *Store) UpdateProposalOutcome(ctx context.Context, project, id, status, reason string, plans []string) error {
	if status != "shown" && status != "selected" && status != "rejected" && status != "deferred" && status != "implemented" && status != "dismissed" && status != "removed" {
		return fmt.Errorf("invalid proposal status %q", status)
	}
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		proposal, err := loadProposalTx(tx, project, id)
		if err != nil {
			return err
		}
		proposal.Status = status
		if status == "rejected" {
			proposal.RejectionReason = strings.TrimSpace(reason)
		} else if status == "deferred" || status == "dismissed" || status == "removed" {
			proposal.DeferralReason = strings.TrimSpace(reason)
		}
		if len(plans) > 0 {
			proposal.ApprovedPlanIDs = append(proposal.ApprovedPlanIDs, plans...)
		}
		return writeProposalTx(tx, proposal, false)
	})
}

// ProposalByFingerprint finds exact normalized matches across all historical outcomes.
func (s *Store) ProposalByFingerprint(ctx context.Context, project, fingerprint string) (model.Proposal, error) {
	var id string
	if err := s.DB.QueryRowContext(ctx, "SELECT proposal_id FROM proposal_fingerprints WHERE project_id=? AND fingerprint=?", project, fingerprint).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Proposal{}, ErrNotFound
		}
		return model.Proposal{}, err
	}
	var data []byte
	if err := s.DB.QueryRowContext(ctx, "SELECT data FROM proposals WHERE project_id=? AND id=?", project, id).Scan(&data); err != nil {
		return model.Proposal{}, err
	}
	var proposal model.Proposal
	err := json.Unmarshal(data, &proposal)
	return proposal, err
}

func (s *Store) ProposalByID(ctx context.Context, project, id string) (model.Proposal, error) {
	var data []byte
	if err := s.DB.QueryRowContext(ctx, "SELECT data FROM proposals WHERE project_id=? AND id=?", project, id).Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Proposal{}, ErrNotFound
		}
		return model.Proposal{}, err
	}
	var proposal model.Proposal
	err := json.Unmarshal(data, &proposal)
	return proposal, err
}

// PendingProposalReconsiderations returns a bounded, project-scoped view of
// current human requests. Stale requests are omitted and cannot authorize work.
func (s *Store) PendingProposalReconsiderations(ctx context.Context, project string, limit int) ([]model.ProposalReconsideration, error) {
	if limit < 1 || limit > 12 {
		return nil, errors.New("proposal reconsideration limit must be between 1 and 12")
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT version,data FROM resources WHERE kind='proposal_reconsiderations' AND json_extract(data,'$.project_id')=? AND json_extract(data,'$.status')='pending' ORDER BY rowid LIMIT ?", project, limit)
	if err != nil {
		return nil, err
	}
	type row struct {
		version int
		data    []byte
	}
	var candidates []row
	for rows.Next() {
		var candidate row
		if err := rows.Scan(&candidate.version, &candidate.data); err != nil {
			_ = rows.Close()
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	rowErr := rows.Err()
	_ = rows.Close()
	if rowErr != nil {
		return nil, rowErr
	}
	requests := make([]model.ProposalReconsideration, 0, len(candidates))
	for _, candidate := range candidates {
		var request model.ProposalReconsideration
		if err := json.Unmarshal(candidate.data, &request); err != nil {
			return nil, err
		}
		if request.ProjectID != project || request.Status != "pending" || request.ID == "" || request.PredecessorID == "" || strings.TrimSpace(request.Reason) == "" {
			continue
		}
		if _, err := s.PendingProposalReconsideration(ctx, project, request.PredecessorID, request.Reason); err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, nil
}
