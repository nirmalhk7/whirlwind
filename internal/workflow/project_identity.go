package workflow

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"whirlwind/internal/model"
)

// localOnlyProject recognizes the canonical checkout-path identity used for
// registrations without a hosted remote. Older hosted projects may only have
// an owner/repository in Repo, so a missing RemoteURL alone is not sufficient.
func localOnlyProject(project model.Project) bool {
	repository := strings.TrimSpace(project.Repo)
	return strings.TrimSpace(project.RemoteURL) == "" && (repository == "" || filepath.IsAbs(repository))
}

// CheckIdeationPrerequisites checks only the sources selected by this workflow.
// Agentic mode uses scoped integration connections rather than the legacy CLI.
func (e *Engine) CheckIdeationPrerequisites(ctx context.Context, project model.Project) error {
	if e.Agentic(ctx) || localOnlyProject(project) {
		return nil
	}
	if e.GitHub == nil {
		return errors.New("GitHub ideation sources require a configured GitHub client")
	}
	if checker, ok := e.GitHub.(interface {
		CheckSources(context.Context, string) error
	}); ok {
		return checker.CheckSources(ctx, project.Repo)
	}
	return nil
}
