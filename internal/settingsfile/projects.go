package settingsfile

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/projectregistry"
	"whirlwind/internal/store"
)

func (c *configurationChanges) projectValues(doc Document) error {
	statuses := make([]model.ProjectStatus, 0, len(doc.Projects))
	seen := map[string]bool{}
	for _, path := range doc.Projects {
		if seen[path] {
			continue
		}
		seen[path] = true
		status, err := c.onboardProject(path)
		if err != nil {
			return err
		}
		statuses = append(statuses, status)
	}
	return c.appendValue("project_status", "configured", statuses)
}

func (c *configurationChanges) onboardProject(path string) (model.ProjectStatus, error) {
	status := model.ProjectStatus{Path: path, Status: "ERR"}
	if !filepath.IsAbs(path) {
		status.Detail = "Absolute local Git repository path required"
		return status, nil
	}
	ctx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
	defer cancel()
	canonical, err := projectregistry.Root(ctx, path)
	if err != nil {
		status.Detail = err.Error()
		return status, c.ctx.Err()
	}
	status.Status = "OK"
	project, err := projectregistry.Load(ctx, filepath.Base(canonical), canonical)
	if err != nil {
		status.Detail = "Onboarding failed: " + err.Error()
		return status, c.ctx.Err()
	}
	status.ProjectID = project.ID
	return status, c.registerProject(project)
}

func (c *configurationChanges) registerProject(project model.Project) error {
	_, err := c.source.store.Get(c.ctx, "projects", project.ID)
	if err == nil {
		// Preserve previously onboarded project choices and runtime state.
		return nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	for _, change := range c.values {
		if change.Kind == "projects" && change.ID == project.ID {
			return nil
		}
	}
	return c.appendValue("projects", project.ID, project)
}
