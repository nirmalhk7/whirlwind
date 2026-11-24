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
	c.checkoutIDs = make(map[string]string, len(doc.Projects))
	statuses := make([]model.ProjectStatus, 0, len(doc.Projects))
	seen := map[string]bool{}
	for _, path := range doc.Projects {
		if seen[path] {
			continue
		}
		seen[path] = true
		status, err := c.onboardProject(path, previous[path], doc.Workers.Ideation.OnRegistrationEnabled())
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

func (c *configurationChanges) onboardProject(path string, previous model.ProjectStatus, ideate bool) (model.ProjectStatus, error) {
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
	if id := c.checkoutIDs[canonical]; id != "" {
		status.Status, status.ProjectID = "OK", id
		return status, nil
	}
	registrations, err := c.checkoutRegistrations(canonical, previous)
	if err != nil {
		return status, err
	}
	var loaded model.Project
	if len(registrations) > 0 && registrations[0].project.ManagedPolicy {
		// A managed policy lives in Whirlwind state, so refresh only Git metadata.
		loaded.Repo, loaded.RemoteURL, err = projectregistry.Origin(ctx, canonical)
	} else {
		loaded, err = projectregistry.Load(ctx, filepath.Base(canonical), canonical)
	}
	if len(registrations) > 0 {
		status.ProjectID = registrations[0].record.ID
	}
	if err != nil {
		status.Detail = "Onboarding failed: " + err.Error()
		return status, c.ctx.Err()
	}
	if len(registrations) == 0 {
		c.registerProject(loaded, ideate)
		status.ProjectID = loaded.ID
	} else {
		c.preserveCheckoutIdentity(registrations, loaded.Repo, loaded.RemoteURL)
	}
	c.checkoutIDs[canonical] = status.ProjectID
	status.Status = "OK"
	return status, nil
}

func (c *configurationChanges) registerProject(project model.Project, ideate bool) {
	project.Status = "draft"
	c.values = append(c.values, store.UnregisteredCheckoutPrecondition(project), store.Change{Kind: "projects", ID: project.ID, Value: project})
	c.values = append(c.values, store.ProjectRegistrationWork(project.ID, time.Now(), ideate)...)
}
