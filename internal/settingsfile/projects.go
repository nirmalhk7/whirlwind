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
	previous, err := c.projectHealthBindings()
	if err != nil {
		return err
	}
	statuses := make([]model.ProjectStatus, 0, len(doc.Projects))
	seen := map[string]bool{}
	for _, path := range doc.Projects {
		if seen[path] {
			continue
		}
		seen[path] = true
		status, err := c.onboardProject(path, previous[path])
		if err != nil {
			return err
		}
		statuses = append(statuses, status)
	}
	return c.appendValue("project_status", "configured", statuses)
}

func (c *configurationChanges) projectHealthBindings() (map[string]model.ProjectStatus, error) {
	bindings := map[string]model.ProjectStatus{}
	record, err := c.source.store.Get(c.ctx, "project_status", "configured")
	if errors.Is(err, store.ErrNotFound) {
		return bindings, nil
	}
	if err != nil {
		return nil, err
	}
	statuses, err := store.Decode[[]model.ProjectStatus](record)
	if err != nil {
		return nil, err
	}
	for _, status := range statuses {
		bindings[status.Path] = status
	}
	return bindings, nil
}

func (c *configurationChanges) onboardProject(path string, previous model.ProjectStatus) (model.ProjectStatus, error) {
	status := model.ProjectStatus{Path: path, Status: "ERR"}
	if !filepath.IsAbs(path) {
		status.Detail = "Absolute local Git repository path required"
		return status, nil
	}
	status.ProjectID = previous.ProjectID
	status.CanonicalPath = previous.CanonicalPath
	ctx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
	defer cancel()
	canonical, err := projectregistry.Root(ctx, path)
	if err != nil {
		status.Detail = err.Error()
		return status, c.ctx.Err()
	}
	// A resolved alias may now point at a different repository.
	status.ProjectID = ""
	status.CanonicalPath = canonical
	project, err := projectregistry.Load(ctx, filepath.Base(canonical), canonical)
	if err != nil {
		status.Detail = "Onboarding failed: " + err.Error()
		return status, c.ctx.Err()
	}
	project, err = c.preserveCheckoutIdentity(project, previous)
	if err != nil {
		return status, err
	}
	status.Status = "OK"
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
	project.Status = "draft"
	if err := c.appendValue("projects", project.ID, project); err != nil {
		return err
	}
	c.values = append(c.values, store.InitialProjectWork(project.ID, time.Now())...)
	return nil
}
