package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"whirlwind/internal/model"
)

func syncProposalTx(ctx context.Context, tx *sql.Tx, direction model.Direction) error {
	created := time.Now().UTC().Format(time.RFC3339Nano)
	proposal := proposalFromDirection(direction, created)
	if proposal.ID == "" || proposal.ProjectID == "" {
		return errors.New("proposal requires direction and project IDs")
	}
	var existingRaw []byte
	err := tx.QueryRowContext(ctx, "SELECT data FROM proposals WHERE project_id=? AND id=?", proposal.ProjectID, proposal.ID).Scan(&existingRaw)
	if err == nil {
		var previous model.Proposal
		if err := json.Unmarshal(existingRaw, &previous); err != nil {
			return err
		}
		proposal.CreatedAt = previous.CreatedAt
		if proposal.RejectionReason == "" {
			proposal.RejectionReason = previous.RejectionReason
		}
		if proposal.DeferralReason == "" {
			proposal.DeferralReason = previous.DeferralReason
		}
		if len(proposal.RelatedProposalIDs) == 0 {
			proposal.RelatedProposalIDs = previous.RelatedProposalIDs
		}
		if len(proposal.ApprovedPlanIDs) == 0 {
			proposal.ApprovedPlanIDs = previous.ApprovedPlanIDs
		}
		return writeProposalTx(tx, proposal, true)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return writeProposalTx(tx, proposal, true)
}

func validProposalRevisit(previous, next model.Proposal) bool {
	if next.PredecessorID != previous.ID || strings.TrimSpace(next.RevisitReason) == "" {
		return false
	}
	if previous.Status == "rejected" && strings.TrimSpace(previous.RejectionReason) != "" && strings.EqualFold(strings.TrimSpace(previous.RejectionReason), strings.TrimSpace(next.RevisitReason)) {
		return false
	}
	return previous.RepositoryRevision != next.RepositoryRevision || !sameProposalEvidence(previous.Evidence, next.Evidence)
}

func publishedRevisitIdentityMatches(previous, next model.Proposal) bool {
	return previous.ID == next.ID && previous.ProjectID == next.ProjectID && previous.Fingerprint == next.Fingerprint && previous.Category == next.Category && previous.Problem == next.Problem && previous.Mechanism == next.Mechanism && strings.Join(previous.Components, "\x00") == strings.Join(next.Components, "\x00") && previous.Title == next.Title && previous.Description == next.Description && previous.PredecessorID == next.PredecessorID && previous.RevisitReason == next.RevisitReason && previous.RepositoryRevision == next.RepositoryRevision && sameProposalEvidence(previous.Evidence, next.Evidence)
}

func (s *Store) PendingProposalReconsideration(ctx context.Context, project, predecessor, reason string) (model.ProposalReconsideration, error) {
	request, _, err := pendingProposalReconsideration(ctx, s.DB, project, predecessor, reason)
	return request, err
}

type proposalReconsiderationQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func pendingProposalReconsideration(ctx context.Context, query proposalReconsiderationQuery, project, predecessor, reason string) (model.ProposalReconsideration, int, error) {
	candidates, err := reconsiderationCandidates(ctx, query, project, predecessor)
	if err != nil {
		return model.ProposalReconsideration{}, 0, err
	}
	for _, candidate := range candidates {
		var request model.ProposalReconsideration
		if err := json.Unmarshal(candidate.data, &request); err != nil {
			return model.ProposalReconsideration{}, 0, err
		}
		if !pendingReconsiderationMatches(request, candidate.id, project, predecessor, reason) {
			continue
		}
		matches, err := reconsiderationPredecessorMatches(ctx, query, request)
		if err != nil {
			return model.ProposalReconsideration{}, 0, err
		}
		if matches {
			return request, candidate.version, nil
		}
	}
	return model.ProposalReconsideration{}, 0, ErrNotFound
}

type reconsiderationRow struct {
	id      string
	version int
	data    []byte
}

func reconsiderationCandidates(ctx context.Context, query proposalReconsiderationQuery, project, predecessor string) ([]reconsiderationRow, error) {
	rows, err := query.QueryContext(ctx, "SELECT id,version,data FROM resources WHERE kind='proposal_reconsiderations' AND json_extract(data,'$.project_id')=? AND json_extract(data,'$.predecessor_id')=? AND json_extract(data,'$.status')='pending' ORDER BY rowid", project, predecessor)
	if err != nil {
		return nil, err
	}
	return readReconsiderationRows(rows)
}

func readReconsiderationRows(rows *sql.Rows) ([]reconsiderationRow, error) {
	// Close this cursor before looking up the matching predecessor using the
	// same database connection or transaction.
	defer func() { _ = rows.Close() }()
	var candidates []reconsiderationRow
	for rows.Next() {
		var candidate reconsiderationRow
		if err := rows.Scan(&candidate.id, &candidate.version, &candidate.data); err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	return candidates, rows.Err()
}

func pendingReconsiderationMatches(request model.ProposalReconsideration, id, project, predecessor, reason string) bool {
	return request.ID == id && request.ProjectID == project && request.PredecessorID == predecessor && request.Status == "pending" && strings.TrimSpace(request.Reason) != "" && (reason == "" || request.Reason == strings.TrimSpace(reason))
}

func reconsiderationPredecessorMatches(ctx context.Context, query proposalReconsiderationQuery, request model.ProposalReconsideration) (bool, error) {
	if request.RequestedIdeaVersion > 0 {
		return reconsiderationIdeaMatches(ctx, query, request)
	}
	var raw []byte
	var version int
	err := query.QueryRowContext(ctx, "SELECT version,data FROM resources WHERE kind='directions' AND id=?", request.PredecessorID).Scan(&version, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var direction model.Direction
	if err := json.Unmarshal(raw, &direction); err != nil {
		return false, err
	}
	return direction.ProjectID == request.ProjectID && direction.Status == "reconsider_requested" && !direction.Selected && request.RequestedDirectionVersion == version, nil
}

func reconsiderationIdeaMatches(ctx context.Context, query proposalReconsiderationQuery, request model.ProposalReconsideration) (bool, error) {
	var raw []byte
	var version int
	err := query.QueryRowContext(ctx, "SELECT version,data FROM resources WHERE kind='ideas' AND id=?", request.PredecessorID).Scan(&version, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var idea model.Idea
	if err := json.Unmarshal(raw, &idea); err != nil {
		return false, err
	}
	return idea.ProjectID == request.ProjectID && idea.ArchivedAt != "" && idea.ReconsiderationID == request.ID && version == request.RequestedIdeaVersion, nil
}

func sameProposalEvidence(left, right []model.EvidenceRef) bool {
	a, _ := json.Marshal(left)
	b, _ := json.Marshal(right)
	return string(a) == string(b)
}

func loadProposalTx(tx *sql.Tx, project, id string) (model.Proposal, error) {
	var data []byte
	if err := tx.QueryRow("SELECT data FROM proposals WHERE project_id=? AND id=?", project, id).Scan(&data); err != nil {
		return model.Proposal{}, err
	}
	var proposal model.Proposal
	err := json.Unmarshal(data, &proposal)
	return proposal, err
}
