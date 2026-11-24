package settingsfile

import (
	"errors"
	"strings"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

// The origin is mutable metadata, while the original checkout record owns
// history, setup qualification, and selection. Never rename or re-onboard
// that record solely because Git now reports a different remote.
func (c *configurationChanges) preserveCheckoutIdentity(loaded model.Project, previous model.ProjectStatus) (model.Project, error) {
	record, saved, err := c.findRegisteredCheckout(loaded.Path, previous)
	if err != nil {
		return model.Project{}, err
	}
	for _, change := range c.values {
		if change.Kind != "projects" {
			continue
		}
		if pending, ok := change.Value.(model.Project); ok && pending.Path == loaded.Path &&
			(record.Version == 0 || change.ID == record.ID) {
			return pending, nil // Another configured alias already staged this checkout.
		}
	}
	if record.Version == 0 {
		return loaded, nil
	}
	repoKey := loaded.Repo
	if short, ok := strings.CutPrefix(loaded.RemoteURL, "https://github.com/"); ok && strings.EqualFold(saved.Repo, short) {
		// Legacy HTTP registration used owner/repo as the issue binding key.
		// Learning its URL doesn't change which repository owns those issues.
		repoKey = saved.Repo
	}
	if saved.Repo != repoKey || saved.RemoteURL != loaded.RemoteURL {
		saved.Repo, saved.RemoteURL = repoKey, loaded.RemoteURL
		// Fence against a human or runner changing the project after this read.
		c.values = append(c.values, store.Change{Kind: "projects", ID: record.ID, Value: saved, Expected: record.Version})
	}
	return saved, nil
}

func (c *configurationChanges) findRegisteredCheckout(path string, previous model.ProjectStatus) (model.Record, model.Project, error) {
	if previous.CanonicalPath == path && previous.ProjectID != "" {
		record, err := c.source.store.Get(c.ctx, "projects", previous.ProjectID)
		if err == nil {
			project, err := store.Decode[model.Project](record)
			if err != nil {
				return model.Record{}, model.Project{}, err
			}
			if project.Path == path && project.ID == record.ID {
				return record, project, nil
			}
		} else if !errors.Is(err, store.ErrNotFound) {
			return model.Record{}, model.Project{}, err
		}
	}
	// A checkout can have been registered by the local control command or be
	// newly listed through an alias with no previous settings binding.
	rows, err := c.source.store.List(c.ctx, "projects")
	if err != nil {
		return model.Record{}, model.Project{}, err
	}
	var oldest model.Record
	var bound model.Project
	for _, record := range rows {
		project, err := store.Decode[model.Project](record)
		if err != nil {
			return model.Record{}, model.Project{}, err
		}
		if project.Path == path && project.ID == record.ID && (oldest.Version == 0 || record.CreatedOrder < oldest.CreatedOrder) {
			oldest, bound = record, project
		}
	}
	return oldest, bound, nil
}
