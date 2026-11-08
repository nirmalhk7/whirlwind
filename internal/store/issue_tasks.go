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
		return ingestIssueTx(ctx, tx, p, i, &t)
	})
	return t, e
}

func ingestIssueTx(ctx context.Context, tx *sql.Tx, project model.Project, issue model.Issue, task *model.Task) error {
	id, e := issueTaskID(ctx, tx, project, issue)
	if errors.Is(e, sql.ErrNoRows) {
		return createIssueTask(ctx, tx, project, issue, task)
	}
	if e != nil {
		return e
	}
	return refreshIngestedIssue(issueTaskRefresh{ctx: ctx, tx: tx, id: id, project: project, issue: issue, task: task})
}

func refreshIngestedIssue(input issueTaskRefresh) error {
	prior, err := loadIssueTask(input.ctx, input.tx, input.id)
	if err != nil {
		return err
	}
	if prior.Cancelled() && input.issue.Eligible() && newCancelledSource(prior.Issue, input.issue) {
		if _, err := input.tx.ExecContext(input.ctx, "DELETE FROM issue_tasks WHERE repo=? AND issue=?", input.project.Repo, input.issue.Number); err != nil {
			return err
		}
		return createIssueTask(input.ctx, input.tx, input.project, input.issue, input.task)
	}
	if err := refreshIssueTask(input); err != nil {
		return err
	}
	return issueCandidate(input.ctx, input.tx, input.project, *input.task)
}

func issueTaskID(ctx context.Context, tx *sql.Tx, project model.Project, issue model.Issue) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, "SELECT task_id FROM issue_tasks WHERE repo=? AND issue=?", project.Repo, issue.Number).Scan(&id)
	return id, err
}

func loadIssueTask(ctx context.Context, tx *sql.Tx, id string) (model.Task, error) {
	var data []byte
	if err := tx.QueryRowContext(ctx, "SELECT data FROM resources WHERE kind='tasks' AND id=?", id).Scan(&data); err != nil {
		return model.Task{}, err
	}
	var task model.Task
	err := json.Unmarshal(data, &task)
	return task, err
}

func newCancelledSource(previous, incoming model.Issue) bool {
	if sameIssueContent(previous, incoming) {
		return false
	}
	before, priorErr := time.Parse(time.RFC3339, previous.UpdatedAt)
	after, nextErr := time.Parse(time.RFC3339, incoming.UpdatedAt)
	if priorErr == nil && nextErr == nil {
		return after.After(before)
	}
	return previous.UpdatedAt == "" || incoming.UpdatedAt != previous.UpdatedAt
}

type issueTaskRefresh struct {
	ctx     context.Context
	tx      *sql.Tx
	id      string
	project model.Project
	issue   model.Issue
	task    *model.Task
}

func refreshIssueTask(input issueTaskRefresh) error {
	var data []byte
	var version int
	if err := input.tx.QueryRowContext(input.ctx, "SELECT data,version FROM resources WHERE kind='tasks' AND id=?", input.id).Scan(&data, &version); err != nil {
		return err
	}
	if err := json.Unmarshal(data, input.task); err != nil {
		return err
	}
	if updateIssueTask(input.task, input.issue) {
		if input.issue.Eligible() && input.task.Stage == "awaiting_selection" {
			input.task.Stage = "planning"
			authorizeReady(input.task, input.project)
		}
		return put(input.ctx, input.tx, "tasks", input.id, *input.task, version)
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

// SaveDirections stores a direction run and records whether all four categories
// were produced in one transaction. Incomplete runs remain visible for recovery.
func (s *Store) SaveDirections(ctx context.Context, project, day string, directions []model.Direction) error {
	groupID := ID()
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		for _, d := range directions {
			if err := insertDirectionIfNew(ctx, tx, project, groupID, d); err != nil {
				return err
			}
		}
		key := project + ":" + day
		var version int
		err := tx.QueryRowContext(ctx, "SELECT version FROM resources WHERE kind='schedules' AND id=?", key).Scan(&version)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		present := map[string]bool{}
		for _, direction := range directions {
			present[direction.Category] = true
		}
		missing := make([]string, 0, 4)
		for _, category := range []string{"expand", "optimize", "harden", "simplify"} {
			if !present[category] {
				missing = append(missing, category)
			}
		}
		stage := "complete"
		if len(directions) != 4 || len(missing) != 0 {
			stage = "incomplete"
		}
		state := map[string]any{"stage": stage, "day": day, "project": project, "missing_categories": missing}
		return put(ctx, tx, "schedules", key, state, version)
	})
}

func insertDirectionIfNew(ctx context.Context, tx *sql.Tx, project, groupID string, direction model.Direction) error {
	direction.ProjectID = project
	if direction.Fingerprint == "" {
		direction.Fingerprint = model.ProposalFingerprint(project, direction.Problem, direction.Mechanism, direction.Components)
	}
	direction.GroupID = groupID
	return put(ctx, tx, "directions", direction.ID, direction, 0)
}

func createIssueTask(ctx context.Context, tx *sql.Tx, p model.Project, i model.Issue, t *model.Task) error {
	if !i.Eligible() {
		return ErrNotFound
	}
	if candidate, _, revision, ok := plannerIssueTask(ctx, tx, p, i); ok {
		*t = candidate
		return bindIssueTask(ctx, tx, p.Repo, *t, revision)
	}
	*t = model.Task{Mode: model.PersonalMode, ID: ID(), ProjectID: p.ID, Issue: i, PlanVersion: 1, Stage: "planning", Handoff: model.Handoff{Version: 1, NextAction: "implement ready issue specification"}}
	authorizeReady(t, p)
	if err := bindIssueTask(ctx, tx, p.Repo, *t, 0); err != nil {
		return err
	}
	return issueCandidate(ctx, tx, p, *t)
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
	if json.Unmarshal(data, &candidate) != nil || !unpublishedPlan(candidate, p.ID) || !candidate.HasSelection() {
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
