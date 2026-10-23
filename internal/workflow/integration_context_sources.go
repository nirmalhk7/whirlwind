package workflow

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

type integrationContextSource struct{ Connection, ID, URL, Revision, Summary, Content string }
type integrationContextCollection struct {
	Scanned []string                   `json:"scanned_integrations"`
	Sources []integrationContextSource `json:"sources"`
}
type integrationContextInventory struct {
	available, witnessed map[string]bool
	owners               map[string]string
}

func integrationContextSources(out provider.Output) (any, []model.EvidenceRef, error) {
	var collection integrationContextCollection
	if err := provider.JSON(out.Text, &collection); err != nil {
		return nil, nil, err
	}
	inventory, err := validateIntegrationContextInventory(out, collection.Scanned)
	if err != nil {
		return nil, nil, err
	}
	refs, now, err := collection.evidenceRefs(out.AttemptID, inventory)
	if err != nil {
		return nil, nil, err
	}
	return collection.evidence(out, now), refs, nil
}

func integrationContextWitnesses(calls []provider.ToolEvidence) (map[string]bool, error) {
	witnessed := map[string]bool{}
	for _, call := range calls {
		if !call.Success {
			return nil, fmt.Errorf("integration %s read failed; check native connection permissions and source scopes, then retry collection", call.Ref)
		}
		witnessed[call.Ref] = true
	}
	return witnessed, nil
}

func validateIntegrationContextInventory(out provider.Output, scannedRefs []string) (integrationContextInventory, error) {
	inventory := integrationContextInventory{available: map[string]bool{}, owners: map[string]string{}}
	var err error
	inventory.witnessed, err = integrationContextWitnesses(out.ToolCalls)
	if err != nil {
		return inventory, err
	}
	scanned := map[string]bool{}
	for _, ref := range scannedRefs {
		scanned[ref] = true
	}
	for _, item := range out.Integrations {
		if !item.Enabled {
			continue
		}
		inventory.available[item.Ref] = true
		inventory.owners[item.Ref] = item.Provider
		if !item.Callable || !scanned[item.Ref] || !inventory.witnessed[item.Ref] {
			return inventory, fmt.Errorf("integration %s was not successfully read; check native connection/scopes", item.Ref)
		}
	}
	return inventory, nil
}

func (collection integrationContextCollection) evidenceRefs(attempt string, inventory integrationContextInventory) ([]model.EvidenceRef, string, error) {
	refs := []model.EvidenceRef{}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, source := range collection.Sources {
		ref, err := source.evidenceRef(attempt, now, inventory)
		if err != nil {
			return nil, "", err
		}
		refs = append(refs, ref)
	}
	return refs, now, nil
}

func (source integrationContextSource) evidenceRef(attempt, now string, inventory integrationContextInventory) (model.EvidenceRef, error) {
	if !inventory.available[source.Connection] || !inventory.witnessed[source.Connection] || source.ID == "" && source.URL == "" {
		return model.EvidenceRef{}, errors.New("context source has no callable connection and native tool evidence")
	}
	id := source.ID
	if id == "" {
		id = source.URL
	}
	return model.EvidenceRef{Provider: inventory.owners[source.Connection], Kind: "integration", Ref: source.Connection + ":" + id, Revision: source.Revision, Summary: source.Summary, Connection: source.Connection, URL: source.URL, RetrievedAt: now, AttemptID: attempt}, nil
}

func (collection integrationContextCollection) evidence(out provider.Output, now string) map[string]any {
	// Native inventories and source lists are sets; runtime map iteration and
	// tool completion order must not manufacture new discovery evidence.
	for i := range out.Integrations {
		sort.Slice(out.Integrations[i].Tools, func(a, b int) bool { return out.Integrations[i].Tools[a].Name < out.Integrations[i].Tools[b].Name })
	}
	sort.Slice(out.Integrations, func(i, j int) bool {
		return out.Integrations[i].Provider+":"+out.Integrations[i].Ref < out.Integrations[j].Provider+":"+out.Integrations[j].Ref
	})
	sort.Slice(collection.Sources, func(i, j int) bool { return jsonText(collection.Sources[i]) < jsonText(collection.Sources[j]) })
	return map[string]any{"inventory": out.Integrations, "sources": collection.Sources, "attempt_id": out.AttemptID, "retrieved_at": now}
}
