package workflow

import (
	"context"
	"errors"
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
	note, err := normalizeMemoryNote(note)
	if err != nil {
		return 0, err
	}
	view, project, err := e.projectMemoryView(ctx, projectID, role)
	if err != nil {
		return 0, err
	}
	if !view.Enabled || view.PolicyHash != policyHash || view.Version != version {
		return 0, store.ErrConflict
	}
	provenance, err := e.memoryLessonSource(ctx, source, projectID, role)
	if err != nil {
		return 0, err
	}
	return e.saveMemoryLesson(ctx, view, project, provenance, note)
}

func normalizeMemoryNote(note string) (string, error) {
	invalid := errors.New("memory note must be printable UTF-8 and 1 to 400 bytes")
	if len(note) > maxLessonBytes || !utf8.ValidString(note) {
		return "", invalid
	}
	note = store.RedactText(strings.Join(strings.Fields(note), " "))
	if note == "" || len(note) > maxLessonBytes || strings.IndexFunc(note, unicode.IsControl) >= 0 {
		return "", invalid
	}
	return note, nil
}

type lessonSource struct {
	attempt model.Attempt
	version int
}

func (e *Engine) memoryLessonSource(ctx context.Context, source, projectID, role string) (lessonSource, error) {
	record, err := e.Store.Get(ctx, "sessions", source)
	if err != nil {
		return lessonSource{}, err
	}
	attempt, err := store.Decode[model.Attempt](record)
	if err != nil || !memorySourceMatches(attempt, source, projectID, role) || !completedMemorySource(attempt) {
		return lessonSource{}, errors.New("memory source must be a completed attempt for the same project and role")
	}
	return lessonSource{attempt: attempt, version: record.Version}, nil
}

func memorySourceMatches(attempt model.Attempt, source, projectID, role string) bool {
	return attempt.AttemptID == source && attempt.ProjectID == projectID && attempt.Role == role
}

func completedMemorySource(attempt model.Attempt) bool {
	if !attempt.Finished || attempt.FinishedAt == "" || attempt.PolicyHash == "" {
		return false
	}
	return attempt.Purpose != "provider_test" && (attempt.Task != "" || attempt.OperationID != "")
}

func (e *Engine) saveMemoryLesson(ctx context.Context, view ProjectMemoryView, project model.Record, provenance lessonSource, note string) (int, error) {
	attempt := provenance.attempt
	source := attempt.AttemptID
	for _, current := range view.Lessons {
		if current.AttemptID == source {
			if current.Note != note {
				return 0, errors.New("a lesson already exists for that attempt")
			}
			return view.Version, nil
		}
	}
	lesson := model.RoleLesson{AttemptID: source, PlanID: attempt.Task, PlanVersion: attempt.PlanVersion, OperationID: attempt.OperationID,
		SourcePolicy: attempt.PolicyHash, SourceCommit: attempt.ContextCommit, RecordedAt: time.Now().UTC().Format(time.RFC3339Nano), Note: note}
	lessons := append([]model.RoleLesson{lesson}, view.Lessons...)
	if len(lessons) > maxRoleLessons {
		lessons = lessons[:maxRoleLessons]
	}
	memory := store.ProjectRoleMemory{Version: view.Version, PolicyHash: view.PolicyHash, PolicyVersion: view.policyVersion, Lessons: lessons}
	err := e.Store.SaveProjectRoleMemory(ctx, attempt.ProjectID, attempt.Role, memory,
		store.VersionPrecondition("projects", project.ID, project.Version),
		store.VersionPrecondition(policy.ManagedPolicyResource, project.ID, view.policyVersion),
		store.VersionPrecondition("sessions", source, provenance.version))
	return view.Version + 1, err
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
