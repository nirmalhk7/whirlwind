package store

import (
	"database/sql"
	"encoding/json"
	"strings"

	"whirlwind/internal/model"
)

func writeProposalTx(tx *sql.Tx, proposal model.Proposal, enforceFingerprint bool) error {
	encoded, components, err := encodeProposal(proposal)
	if err != nil {
		return err
	}
	if err := authorizeProposalWrite(tx, proposal, enforceFingerprint); err != nil {
		return err
	}
	if err := upsertProposalTx(tx, proposal, encoded, components); err != nil {
		return err
	}
	if err := replaceProposalIndexesTx(tx, proposal); err != nil {
		return err
	}
	return saveProposalFingerprintTx(tx, proposal, enforceFingerprint)
}

func encodeProposal(proposal model.Proposal) (string, string, error) {
	encoded, err := json.Marshal(proposal)
	if err != nil {
		return "", "", err
	}
	components, err := json.Marshal(proposal.Components)
	return string(encoded), string(components), err
}

func upsertProposalTx(tx *sql.Tx, proposal model.Proposal, encoded, components string) error {
	_, err := tx.Exec("INSERT INTO proposals(project_id,id,category,problem,mechanism,components,revision,status,fingerprint,title,created_at,data) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(project_id,id) DO UPDATE SET category=excluded.category,problem=excluded.problem,mechanism=excluded.mechanism,components=excluded.components,revision=excluded.revision,status=excluded.status,fingerprint=excluded.fingerprint,title=excluded.title,data=excluded.data", proposal.ProjectID, proposal.ID, proposal.Category, proposal.Problem, proposal.Mechanism, components, proposal.RepositoryRevision, proposal.Status, proposal.Fingerprint, proposal.Title, proposal.CreatedAt, encoded)
	return err
}

func replaceProposalIndexesTx(tx *sql.Tx, proposal model.Proposal) error {
	if err := replaceProposalComponentsTx(tx, proposal); err != nil {
		return err
	}
	return replaceProposalSearchTx(tx, proposal)
}

func replaceProposalComponentsTx(tx *sql.Tx, proposal model.Proposal) error {
	if _, err := tx.Exec("DELETE FROM proposal_components WHERE project_id=? AND proposal_id=?", proposal.ProjectID, proposal.ID); err != nil {
		return err
	}
	for _, component := range proposal.Components {
		component = strings.TrimSpace(component)
		if component == "" {
			continue
		}
		if _, err := tx.Exec("INSERT OR IGNORE INTO proposal_components(project_id,component,proposal_id) VALUES(?,?,?)", proposal.ProjectID, component, proposal.ID); err != nil {
			return err
		}
	}
	return nil
}

func replaceProposalSearchTx(tx *sql.Tx, proposal model.Proposal) error {
	if _, err := tx.Exec("DELETE FROM proposals_fts WHERE project_id=? AND proposal_id=?", proposal.ProjectID, proposal.ID); err != nil {
		return err
	}
	terms := strings.Join([]string{proposal.Title, proposal.Problem, proposal.Mechanism, strings.Join(proposal.Components, " "), proposal.Description, proposal.Status, proposal.RejectionReason, proposal.DeferralReason, strings.Join(proposal.ApprovedPlanIDs, " "), strings.Join(proposal.RelatedProposalIDs, " ")}, " ")
	_, err := tx.Exec("INSERT INTO proposals_fts(project_id,proposal_id,terms) VALUES(?,?,?)", proposal.ProjectID, proposal.ID, terms)
	return err
}

func saveProposalFingerprintTx(tx *sql.Tx, proposal model.Proposal, enforceFingerprint bool) error {
	if proposal.Fingerprint == "" {
		return nil
	}
	query := "INSERT OR IGNORE INTO proposal_fingerprints(project_id,fingerprint,proposal_id,reason,kind,created_at) VALUES(?,?,?,?,?,?)"
	if enforceFingerprint {
		query = "INSERT INTO proposal_fingerprints(project_id,fingerprint,proposal_id,reason,kind,created_at) VALUES(?,?,?,?,?,?) ON CONFLICT(project_id,fingerprint) DO UPDATE SET proposal_id=excluded.proposal_id,reason=excluded.reason,kind=excluded.kind,created_at=excluded.created_at"
	}
	_, err := tx.Exec(query, proposal.ProjectID, proposal.Fingerprint, proposal.ID, proposal.RejectionReason, proposal.Status, proposal.CreatedAt)
	return err
}
