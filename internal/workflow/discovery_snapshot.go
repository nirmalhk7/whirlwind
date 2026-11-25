package workflow

import (
	"maps"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	ctxengine "whirlwind/internal/context"
	"whirlwind/internal/model"
)

func indexedDiscoveryChanges(index ctxengine.Index, previous discoverySnapshot, snapshot *discoverySnapshot) ([]map[string]any, []string) {
	changed := []map[string]any{}
	paths := slices.AppendSeq(make([]string, 0, len(index.Files)), maps.Keys(index.Files))
	slices.Sort(paths)
	for _, path := range paths {
		entry := index.Files[path]
		fingerprint := model.Fingerprint([]byte(entry.Text))
		snapshot.Files[path] = fingerprint
		if previous.Files[path] != fingerprint {
			revision, summary := discoveryEvidenceRevision(index, path, fingerprint)
			snapshot.Evidence = append(snapshot.Evidence, model.EvidenceRef{Kind: "code", Ref: path, Revision: revision, Summary: summary})
			if len(changed) < 24 {
				changed = append(changed, discoveryEntryContext(path, entry))
			}
		}
	}
	appendRemovedDiscoveryEvidence(index, previous, snapshot)
	return changed, paths
}

func appendRemovedDiscoveryEvidence(index ctxengine.Index, previous discoverySnapshot, snapshot *discoverySnapshot) {
	for path := range previous.Files {
		if _, exists := snapshot.Files[path]; exists || index.WorkingTreeExcluded[path] {
			continue
		}
		revision, summary := index.Commit, "Removed indexed source"
		if index.WorkingTreeDeleted[path] {
			revision = "worktree-deleted:" + index.Commit
			summary = "Deleted from the current working tree"
		}
		snapshot.Evidence = append(snapshot.Evidence, model.EvidenceRef{Kind: "code", Ref: path, Revision: revision, Summary: summary})
	}
}

func discoveryEntryContext(path string, entry ctxengine.Entry) map[string]any {
	return map[string]any{"path": path, "symbols": entry.Symbols, "todos": entry.TODOs, "action_markers": entry.ActionMarkers, "excerpt": boundedDiscoveryExcerpt(entry.Text)}
}

func boundedDiscoveryExcerpt(source string) string {
	const maxExcerptBytes = 720
	if len(source) <= maxExcerptBytes {
		return source
	}
	end := maxExcerptBytes
	for end > 0 && !utf8.RuneStart(source[end]) {
		end--
	}
	return source[:end] + "\n[Current source excerpt truncated; inspect the pinned read-only checkout for complete code]"
}

func discoveryFingerprint(snapshot discoverySnapshot) string {
	identity := struct {
		Commit                string
		Files                 map[string]string
		Sources               string
		ApprovedPlans         []approvedPlanContext
		HumanReconsiderations []model.ProposalReconsideration
		ExplorationAreas      []string
		Brief                 model.IdeationBrief
		Diagnostics           map[string]string
		PolicyHash            string
		MaxCandidates         int
	}{snapshot.Commit, snapshot.Files, stableIntegrationContext(snapshot.Sources), snapshot.ApprovedPlans, snapshot.HumanReconsiderations, snapshot.ExplorationAreas, snapshot.Brief, snapshot.Diagnostics, snapshot.PolicyHash, snapshot.MaxCandidates}
	return model.Fingerprint([]byte(jsonText(identity)))
}

func addDiscoveryRepositoryContext(changed []map[string]any, paths []string, index ctxengine.Index, snapshot *discoverySnapshot) []map[string]any {
	limit := 12
	changedByPath := make(map[string]map[string]any, len(changed))
	changedPaths := make([]string, 0, len(changed))
	for _, item := range changed {
		path, _ := item["path"].(string)
		if path == "" {
			continue
		}
		changedByPath[path] = item
		changedPaths = append(changedPaths, path)
	}
	candidates := append(append([]string{}, paths...), changedPaths...)
	selected := rotatingDiscoveryAreas(candidates, nil, limit)
	result := make([]map[string]any, 0, limit)
	for _, path := range selected {
		if item, changed := changedByPath[path]; changed {
			result = append(result, item)
		} else if entry, exists := index.Files[path]; exists {
			result = append(result, discoveryEntryContext(path, entry))
			if !hasCodeEvidence(snapshot.Evidence, path) {
				revision, summary := discoveryEvidenceRevision(index, path, model.Fingerprint([]byte(entry.Text)))
				snapshot.Evidence = append(snapshot.Evidence, model.EvidenceRef{Kind: "code", Ref: path, Revision: revision, Summary: summary})
			}
		} else {
			continue
		}
	}
	return result
}

func discoveryEvidenceRevision(index ctxengine.Index, path, fingerprint string) (string, string) {
	if _, changed := index.WorkingTreeFiles[path]; changed {
		return "worktree:" + index.Commit + ":" + fingerprint, "Current working-tree source"
	}
	return index.Commit, "Current indexed source for exploration"
}

func hasCodeEvidence(evidence []model.EvidenceRef, path string) bool {
	for _, ref := range evidence {
		if ref.Kind == "code" && ref.Ref == path {
			return true
		}
	}
	return false
}

func boundedSources(sources string) string {
	// Keep valid source JSON available in the persisted snapshot; bound model context only.
	if len(sources) > 24000 {
		end := 24000
		for end > 0 && !utf8.RuneStart(sources[end]) {
			end--
		}
		return sources[:end] + "\n[Source excerpt truncated; do not infer missing evidence]"
	}
	return sources
}

func relevantDirections(history []model.Direction, changed []map[string]any, brief model.IdeationBrief) []model.Direction {
	query := strings.ToLower(brief.Goal + " " + brief.Constraints + " " + brief.Priorities + " " + jsonText(changed))
	sort.SliceStable(history, func(i, j int) bool { return relevance(history[i], query) > relevance(history[j], query) })
	if len(history) > 20 {
		history = history[:20]
	}
	return history
}
