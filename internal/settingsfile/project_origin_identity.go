package settingsfile

import (
	"cmp"
	"slices"
	"strings"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

type checkoutRegistration struct {
	record  model.Record
	project model.Project
}

// checkoutRegistrations finds only registrations of the same local checkout.
// The saved configuration binding is preferred, or the oldest registration
// when this path has never had a saved binding.
func (c *configurationChanges) checkoutRegistrations(path string, previous model.ProjectStatus) ([]checkoutRegistration, error) {
	if c.registeredByCheckout == nil {
		if err := c.loadCheckoutRegistrations(); err != nil {
			return nil, err
		}
	}
	matches := c.registeredByCheckout[path]
	slices.SortFunc(matches, func(a, b checkoutRegistration) int {
		return cmp.Compare(a.record.CreatedOrder, b.record.CreatedOrder)
	})
	if previous.CanonicalPath == path {
		for index, entry := range matches {
			if entry.record.ID == previous.ProjectID {
				matches[0], matches[index] = entry, matches[0]
				break
			}
		}
	}
	return matches, nil
}

func (c *configurationChanges) loadCheckoutRegistrations() error {
	rows, err := c.source.store.List(c.ctx, "projects")
	if err != nil {
		return err
	}
	c.registeredByCheckout = make(map[string][]checkoutRegistration)
	for _, record := range rows {
		project, err := store.Decode[model.Project](record)
		if err != nil {
			return err
		}
		if project.ID == record.ID {
			c.registeredByCheckout[project.Path] = append(c.registeredByCheckout[project.Path], checkoutRegistration{record: record, project: project})
		}
	}
	return nil
}

func (c *configurationChanges) preserveCheckoutIdentity(registrations []checkoutRegistration, repo, remote string) {
	canonical := registrations[0]
	project := canonical.project
	if short, ok := strings.CutPrefix(remote, "https://github.com/"); ok &&
		(strings.EqualFold(project.Repo, short) || strings.EqualFold(project.Repo, repo)) {
		// Legacy HTTP registrations used owner/repo as the issue binding key.
		repo = project.Repo
	}
	for _, duplicate := range registrations[1:] {
		project.CheckoutAliases = appendProjectAlias(project.CheckoutAliases, duplicate.record.ID)
		for _, alias := range duplicate.project.CheckoutAliases {
			if alias != project.ID {
				project.CheckoutAliases = appendProjectAlias(project.CheckoutAliases, alias)
			}
		}
		c.values = append(c.values, store.ArchiveDuplicateProject(duplicate.record))
	}
	if project.Repo != repo || project.RemoteURL != remote || len(registrations) > 1 {
		project.Repo, project.RemoteURL = repo, remote
		c.values = append(c.values, store.Change{Kind: "projects", ID: project.ID, Value: project, Expected: canonical.record.Version})
	}
}

func appendProjectAlias(aliases []string, id string) []string {
	if !slices.Contains(aliases, id) {
		aliases = append(aliases, id)
	}
	return aliases
}
