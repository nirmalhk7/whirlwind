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

// IngestIssue deduplicates repository issues, binds published planner tasks, pauses withdrawn issues, and invalidates stale plans after edits.
func (s *Store) IngestIssue(ctx context.Context, p model.Project, i model.Issue) (model.Task, error) {
	var t model.Task
	e := s.Transaction(ctx, func(tx *sql.Tx) error {
		var id string
		e := tx.QueryRowContext(ctx, "SELECT task_id FROM issue_tasks WHERE repo=? AND issue=?", p.Repo, i.Number).Scan(&id)
		if errors.Is(e, sql.ErrNoRows) {
			return createIssueTask(ctx, tx, p, i, &t)
		}
		if e != nil {
			return e
		}
		return refreshIssueTask(ctx, tx, id, i, &t)
	})
	return t, e
}

func refreshIssueTask(ctx context.Context, tx *sql.Tx, id string, issue model.Issue, task *model.Task) error {
	var data []byte
	var version int
	if err := tx.QueryRowContext(ctx, "SELECT data,version FROM resources WHERE kind='tasks' AND id=?", id).Scan(&data, &version); err != nil {
		return err
	}
	if err := json.Unmarshal(data, task); err != nil {
		return err
	}
	if updateIssueTask(task, issue) {
		return put(ctx, tx, "tasks", id, *task, version)
	}
	return nil
}

// BindIssue atomically associates a planner task with its published GitHub issue.
func (s *Store) BindIssue(ctx context.Context, repo string, t model.Task, expected int) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		var other string
		err := tx.QueryRowContext(ctx, "SELECT task_id FROM issue_tasks WHERE repo=? AND issue=?", repo, t.Issue.Number).Scan(&other)
		if err == nil && other != t.ID {
			return ErrConflict
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO issue_tasks(repo,issue,task_id) VALUES(?,?,?)", repo, t.Issue.Number, t.ID); err != nil {
			return err
		}
		return put(ctx, tx, "tasks", t.ID, t, expected)
	})
}

// SaveDirections stores the generated directions and marks their project/day schedule complete in one transaction.
func (s *Store) SaveDirections(ctx context.Context, project, day string, directions []model.Direction) error {
	groupID := ID()
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		for _, d := range directions {
			d.GroupID = groupID
			if err := put(ctx, tx, "directions", d.ID, d, 0); err != nil {
				return err
			}
		}
		key := project + ":" + day
		var version int
		err := tx.QueryRowContext(ctx, "SELECT version FROM resources WHERE kind='schedules' AND id=?", key).Scan(&version)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return put(ctx, tx, "schedules", key, map[string]string{"stage": "complete", "day": day, "project": project}, version)
	})
}

func createIssueTask(ctx context.Context, tx *sql.Tx, p model.Project, i model.Issue, t *model.Task) error {
	if !i.Eligible() {
		return ErrNotFound
	}
	if candidate, _, revision, ok := plannerIssueTask(ctx, tx, p, i); ok {
		*t = candidate
		return bindIssueTask(ctx, tx, p.Repo, *t, revision)
	}
	*t = model.Task{ID: ID(), ProjectID: p.ID, Issue: i, PlanVersion: 1, Stage: "planning", Handoff: model.Handoff{Version: 1, NextAction: "plan issue"}}
	return bindIssueTask(ctx, tx, p.Repo, *t, 0)
}

func plannerIssueTask(ctx context.Context, tx *sql.Tx, p model.Project, i model.Issue) (model.Task, string, int, bool) {
	marker := "<!-- whirlwind-plan:"
	begin := strings.Index(i.Body, marker)
	if begin < 0 {
		return model.Task{}, "", 0, false
	}
	rest := i.Body[begin+len(marker):]
	end := strings.Index(rest, " -->")
	if end < 0 {
		return model.Task{}, "", 0, false
	}
	var data []byte
	var revision int
	if err := tx.QueryRowContext(ctx, "SELECT data,version FROM resources WHERE kind='tasks' AND id=?", rest[:end]).Scan(&data, &revision); err != nil {
		return model.Task{}, "", 0, false
	}
	var candidate model.Task
	if json.Unmarshal(data, &candidate) != nil || !unpublishedPlan(candidate, p.ID) {
		return model.Task{}, "", 0, false
	}
	candidate.Issue = i
	candidate.Stage = "coding"
	return candidate, rest[:end], revision, true
}

func unpublishedPlan(task model.Task, projectID string) bool {
	return task.ProjectID == projectID && task.Issue.Number == 0 && task.Stage == "plan_ready" && task.Plan != nil
}

func bindIssueTask(ctx context.Context, tx *sql.Tx, repo string, t model.Task, revision int) error {
	if _, err := tx.ExecContext(ctx, "INSERT INTO issue_tasks(repo,issue,task_id) VALUES(?,?,?)", repo, t.Issue.Number, t.ID); err != nil {
		return err
	}
	return put(ctx, tx, "tasks", t.ID, t, revision)
}

func updateIssueTask(t *model.Task, i model.Issue) bool {
	incoming, parseIncoming := time.Parse(time.RFC3339, i.UpdatedAt)
	existing, parseExisting := time.Parse(time.RFC3339, t.Issue.UpdatedAt)
	if parseIncoming == nil && parseExisting == nil && incoming.Before(existing) {
		return false
	}
	if !i.Eligible() {
		return withdrawIssueTask(t, i)
	}
	withdrawn := !t.Issue.Eligible() && t.Stage == "paused" && t.Error == "Issue closed or whirlwind:plan removed"
	if withdrawn && sameIssueContent(t.Issue, i) {
		t.Stage = t.ResumeStage
		if t.Stage == "" {
			t.Stage = "planning"
		}
		t.Issue = i
		t.Error = ""
		t.EligibilityOverride = false
		return true
	}
	if t.Issue.UpdatedAt == i.UpdatedAt && sameIssueContent(t.Issue, i) {
		return false
	}
	invalidateIssueTask(t)
	t.Issue = i
	return true
}

func withdrawIssueTask(t *model.Task, i model.Issue) bool {
	if t.EligibilityOverride && sameIssueContent(t.Issue, i) {
		return false
	}
	if t.Stage == "paused" {
		return false
	}
	t.ResumeStage = t.Stage
	t.Stage = "paused"
	t.Error = "Issue closed or whirlwind:plan removed"
	t.Issue = i
	return true
}

func sameIssueContent(a, b model.Issue) bool {
	return a.Body == b.Body && a.Title == b.Title
}

func invalidateIssueTask(t *model.Task) {
	t.ImplementationAttempts = nil
	t.Approval = nil
	t.VerificationID = ""
	t.ReviewID = ""
	t.LastAttemptID = ""
	if t.PR != "" {
		t.Error = "Issue changed after PR creation; review published PR in GitHub"
		t.ResumeStage = t.Stage
		t.Stage = "paused"
	} else {
		t.PlanVersion++
		t.ResumeStage = ""
		t.EligibilityOverride = false
		t.ReviewedCommit = ""
		t.Plan = nil
		t.Worktree = ""
		t.Branch = ""
		t.Base = ""
		t.ReviewPasses = 0
		t.Recoveries = 0
		t.UnchangedFailures = 0
		t.Fingerprint = ""
		t.Stage = "planning"
		t.ChecksPassed = false
		t.Approved = false
		t.Score = 0
		t.Handoff = model.Handoff{Version: 1, NextAction: "replan updated issue"}
	}

}
