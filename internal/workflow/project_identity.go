package workflow

import (
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
