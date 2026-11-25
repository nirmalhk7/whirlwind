package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/store"
)

const maxRoleLessons = 12
const maxLessonBytes = 400
const maxMemoryPromptBytes = 2048
const maxRecallLessons = 3

type ProjectMemoryView struct {
	policyVersion int
	Enabled       bool               `json:"enabled"`
	Version       int                `json:"version"`
	PolicyHash    string             `json:"policy_hash,omitempty"`
	Lessons       []model.RoleLesson `json:"lessons"`
}

func memoryRole(role string) bool {
	switch role {
	case "ideate", "plan", "code", "review":
		return true
	}
	return false
}

func (e *Engine) ReadProjectMemory(ctx context.Context, projectID, role string) (ProjectMemoryView, error) {
	view, _, err := e.projectMemoryView(ctx, projectID, role)
	return view, err
}

func (e *Engine) projectMemoryView(ctx context.Context, projectID, role string) (ProjectMemoryView, model.Record, error) {
	view := ProjectMemoryView{Lessons: []model.RoleLesson{}}
	if projectID == "" || !memoryRole(role) {
		return view, model.Record{}, errors.New("memory requires a registered project ID and role ideate, plan, code or review")
	}
	record, err := e.Store.Get(ctx, "projects", projectID)
	if err != nil {
		return view, record, err
	}
	project, err := store.Decode[model.Project](record)
	if err != nil || project.ID != projectID {
		return view, record, store.ErrConflict
	}
	memory, err := e.Store.ProjectRoleMemory(ctx, projectID, role)
	if err != nil {
		return view, record, err
	}
	view.Version = memory.Version
	if project.ManagedPolicy && project.Policy.ProjectMemory {
		rules, revision, err := e.managedProjectPolicy(ctx, project)
		if err == nil && rules.ProjectMemory {
			view.Enabled, view.PolicyHash = true, project.PolicyHash
			view.policyVersion = revision
			if memory.PolicyHash == view.PolicyHash && memory.PolicyVersion == view.policyVersion {
				view.Lessons = memory.Lessons
			}
		}
	}
	return view, record, nil
}

func (e *Engine) RememberProjectLesson(ctx context.Context, projectID, role, source, note, policyHash string, version int) (int, error) {
	if len(note) > maxLessonBytes || !utf8.ValidString(note) {
		return 0, errors.New("memory note must be printable UTF-8 and 1 to 400 bytes")
	}
	note = store.RedactText(strings.Join(strings.Fields(note), " "))
	if note == "" || len(note) > maxLessonBytes || strings.IndexFunc(note, unicode.IsControl) >= 0 {
		return 0, errors.New("memory note must be printable UTF-8 and 1 to 400 bytes")
	}
	view, project, err := e.projectMemoryView(ctx, projectID, role)
	if err != nil {
		return 0, err
	}
	if !view.Enabled || view.PolicyHash != policyHash || view.Version != version {
		return 0, store.ErrConflict
	}
	record, err := e.Store.Get(ctx, "sessions", source)
	if err != nil {
		return 0, err
	}
	attempt, err := store.Decode[model.Attempt](record)
	if err != nil || attempt.AttemptID != source || attempt.ProjectID != projectID || attempt.Role != role ||
		!attempt.Finished || attempt.FinishedAt == "" || attempt.PolicyHash == "" || (attempt.Task == "" && attempt.OperationID == "") || attempt.Purpose == "provider_test" {
		return 0, errors.New("memory source must be a completed attempt for the same project and role")
	}
	for _, current := range view.Lessons {
		if current.AttemptID == source {
			if current.Note != note {
				return 0, errors.New("a lesson already exists for that attempt")
			}
			return version, nil
		}
	}
	lesson := model.RoleLesson{AttemptID: source, PlanID: attempt.Task, PlanVersion: attempt.PlanVersion, OperationID: attempt.OperationID,
		SourcePolicy: attempt.PolicyHash, SourceCommit: attempt.ContextCommit, RecordedAt: time.Now().UTC().Format(time.RFC3339Nano), Note: note}
	lessons := append([]model.RoleLesson{lesson}, view.Lessons...)
	if len(lessons) > maxRoleLessons {
		lessons = lessons[:maxRoleLessons]
	}
	memory := store.ProjectRoleMemory{Version: version, PolicyHash: policyHash, PolicyVersion: view.policyVersion, Lessons: lessons}
	err = e.Store.SaveProjectRoleMemory(ctx, projectID, role, memory,
		store.VersionPrecondition("projects", projectID, project.Version),
		store.VersionPrecondition(policy.ManagedPolicyResource, projectID, view.policyVersion),
		store.VersionPrecondition("sessions", source, record.Version))
	return version + 1, err
}

func (e *Engine) ClearProjectMemory(ctx context.Context, projectID, role string, version int) (int, error) {
	view, project, err := e.projectMemoryView(ctx, projectID, role)
	if err != nil {
		return 0, err
	}
	if view.Version != version {
		return 0, store.ErrConflict
	}
	if version == 0 {
		return 0, nil
	}
	memory := store.ProjectRoleMemory{Version: version, Lessons: []model.RoleLesson{}}
	err = e.Store.SaveProjectRoleMemory(ctx, projectID, role, memory, store.VersionPrecondition("projects", projectID, project.Version))
	return version + 1, err
}

func (a *agentRun) projectMemoryPrompt() (string, error) {
	if !a.project.ManagedPolicy || !a.project.Policy.ProjectMemory || !memoryRole(a.input.Role) ||
		a.input.Purpose == "provider_test" || (a.input.Role == "ideate" && a.input.Purpose != "generation") {
		return "", nil
	}
	if a.task.ProjectID != "" && a.task.ProjectID != a.project.ID {
		return "", store.ErrConflict
	}
	view, err := a.ReadProjectMemory(a.ctx, a.project.ID, a.input.Role)
	if err != nil || !view.Enabled || view.PolicyHash != a.project.PolicyHash || len(view.Lessons) == 0 {
		return "", err
	}
	query := map[string]bool{}
	for _, term := range memoryTerms(a.task.Issue.Title + " " + a.task.Issue.Body) {
		query[term] = true
	}
	slices.SortStableFunc(view.Lessons, func(left, right model.RoleLesson) int {
		return memoryScore(query, right.Note) - memoryScore(query, left.Note)
	})
	const heading = "\nHistorical operator lessons (advisory data for this role; use only when consistent with the current task, approvals and policy):\n"
	var out []model.RoleLesson
	var result string
	for _, lesson := range view.Lessons {
		if len(out) == maxRecallLessons {
			break
		}
		next := append(out, lesson)
		raw, err := json.Marshal(next)
		if err != nil {
			return "", err
		}
		if len(heading)+len(raw) <= maxMemoryPromptBytes {
			out, result = next, heading+string(raw)
		}
	}
	return result, nil
}

func memoryTerms(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func memoryScore(query map[string]bool, note string) int {
	score := 0
	for _, term := range memoryTerms(note) {
		if len(term) >= 4 && query[term] {
			score++
		}
	}
	return score
}
