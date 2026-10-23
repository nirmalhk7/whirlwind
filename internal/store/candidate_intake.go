package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"whirlwind/internal/model"
)

// issueCandidate is written in the same transaction as its held task and issue identity.
func issueCandidate(ctx context.Context, tx *sql.Tx, p model.Project, t model.Task) error {
	if t.HasSelection() {
		return nil
	}
	id := "issue-" + model.Fingerprint([]byte(p.Repo + ":" + t.ID))[:24]
	var data []byte
	var version int
	err := tx.QueryRowContext(ctx, "SELECT data,version FROM resources WHERE kind='directions' AND id=?", id).Scan(&data, &version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var previous model.Direction
	if err == nil {
		if err = json.Unmarshal(data, &previous); err != nil {
			return err
		}
	}
	fingerprint := model.IssueFingerprint(t.Issue)
	if previous.Fingerprint == fingerprint {
		return nil
	}
	status := "proposed"
	if !t.Issue.Eligible() {
		status = "deferred"
	}
	value := model.Direction{ID: id, ProjectID: p.ID, TaskID: t.ID, Title: t.Issue.Title, Description: t.Issue.Body, Status: status, SourceIssue: &t.Issue, Fingerprint: fingerprint,
		Scope: []string{t.Issue.Title}, Evidence: []model.EvidenceRef{{Kind: "github_issue", Ref: t.Issue.URL, Revision: t.Issue.UpdatedAt, Summary: t.Issue.Title}}}
	return put(ctx, tx, "directions", id, value, version)
}
