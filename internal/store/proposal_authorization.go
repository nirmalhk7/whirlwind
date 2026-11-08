package store

import (
	"context"
	"database/sql"
	"errors"

	"whirlwind/internal/model"
)

func authorizeProposalWrite(tx *sql.Tx, proposal model.Proposal, enforceFingerprint bool) error {
	if !enforceFingerprint {
		return nil
	}
	if proposal.PredecessorID != "" {
		if err := authorizeProposalRevisit(tx, proposal); err != nil {
			return err
		}
	}
	return authorizeProposalFingerprint(tx, proposal)
}

func authorizeProposalRevisit(tx *sql.Tx, proposal model.Proposal) error {
	published, err := loadProposalTx(tx, proposal.ProjectID, proposal.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && publishedRevisitIdentityMatches(published, proposal) {
		return nil
	}
	previous, err := loadProposalTx(tx, proposal.ProjectID, proposal.PredecessorID)
	if err != nil {
		return ErrConflict
	}
	return authorizeProposalReconsideration(tx, previous, proposal)
}

func authorizeProposalReconsideration(tx *sql.Tx, previous, proposal model.Proposal) error {
	ctx := context.Background()
	request, version, err := pendingProposalReconsideration(ctx, tx, proposal.ProjectID, previous.ID, proposal.RevisitReason)
	if err == nil {
		request.Status, request.CandidateID = "consumed", proposal.ID
		return put(ctx, tx, "proposal_reconsiderations", request.ID, request, version)
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	if !validProposalRevisit(previous, proposal) {
		return ErrConflict
	}
	return nil
}

func authorizeProposalFingerprint(tx *sql.Tx, proposal model.Proposal) error {
	if proposal.Fingerprint == "" {
		return nil
	}
	var owner string
	err := tx.QueryRow("SELECT proposal_id FROM proposal_fingerprints WHERE project_id=? AND fingerprint=?", proposal.ProjectID, proposal.Fingerprint).Scan(&owner)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if owner != proposal.ID && proposal.PredecessorID != owner {
		return ErrConflict
	}
	return nil
}
