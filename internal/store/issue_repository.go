package store

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
)

var githubIssueRepository = regexp.MustCompile(`^[a-z0-9-]+/[a-z0-9_.-]+$`)

// Legacy GitHub registrations used owner/repo. Configured checkouts used a
// canonical URL. Keep both spellings attached to the same repository issue.
func issueBinding(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, repo string, number int) (string, string, error) {
	var key, planID string
	err := db.QueryRowContext(ctx, "SELECT plan_id FROM issue_plans WHERE repo=? AND issue=?", repo, number).Scan(&planID)
	if !errors.Is(err, sql.ErrNoRows) {
		return repo, planID, err
	}
	slug := strings.TrimPrefix(strings.ToLower(repo), "https://github.com/")
	if !githubIssueRepository.MatchString(slug) {
		return "", "", err
	}
	err = db.QueryRowContext(ctx, `SELECT repo,plan_id FROM issue_plans
		WHERE issue=? AND lower(repo) IN (?,?)
		ORDER BY repo LIMIT 1`, number, slug, "https://github.com/"+slug).Scan(&key, &planID)
	return key, planID, err
}
