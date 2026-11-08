package workflow

import (
	"sort"
	"strings"
	"unicode/utf8"
	ctxengine "whirlwind/internal/context"
	"whirlwind/internal/model"
)

func indexedDiscoveryChanges(index ctxengine.Index, previous discoverySnapshot, snapshot *discoverySnapshot) ([]map[string]any, []string) {
	changed := []map[string]any{}
	paths := make([]string, 0, len(index.Files))
	for path := range index.Files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		entry := index.Files[path]
		fingerprint := model.Fingerprint([]byte(entry.Text))
		snapshot.Files[path] = fingerprint
		if previous.Files[path] != fingerprint {
			snapshot.Evidence = append(snapshot.Evidence, model.EvidenceRef{Kind: "code", Ref: path, Revision: index.Commit, Summary: "Changed indexed source"})
			if len(changed) < 24 {
				changed = append(changed, discoveryEntryContext(path, entry))
			}
		}
	}
	for path := range previous.Files {
		if _, exists := snapshot.Files[path]; !exists {
			snapshot.Evidence = append(snapshot.Evidence, model.EvidenceRef{Kind: "code", Ref: path, Revision: index.Commit, Summary: "Removed indexed source"})
		}
	}
	return changed, paths
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

func addFreshDiscoveryContext(fresh bool, changed []map[string]any, paths []string, index ctxengine.Index, snapshot *discoverySnapshot) []map[string]any {
	if !fresh || len(changed) != 0 {
		return changed
	}
	for _, path := range paths[:min(len(paths), 24)] {
		entry := index.Files[path]
		changed = append(changed, discoveryEntryContext(path, entry))
		snapshot.Evidence = append(snapshot.Evidence, model.EvidenceRef{Kind: "code", Ref: path, Revision: index.Commit, Summary: "Explicit fresh exploration"})
	}
	return changed
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
