package workflow

import (
	"sort"

	ctxengine "whirlwind/internal/context"
	"whirlwind/internal/model"
)

const ideationRepositoryContextLimit = 24000

type ideationRepositoryContext struct {
	IndexedPaths           []string                   `json:"indexed_paths"`
	IndexedPathsComplete   bool                       `json:"indexed_paths_complete"`
	Sources                []ideationRepositorySource `json:"sources"`
	SourceCoverageComplete bool                       `json:"source_coverage_complete"`
}

func ideationRepositoryIndexJSON(changed []map[string]any) string {
	metadata := ideationRepositoryIndex{Complete: true, Entries: make([]map[string]any, 0, len(changed))}
	for _, item := range changed {
		entry := make(map[string]any, len(item))
		for key, value := range item {
			if key != "excerpt" {
				entry[key] = value
			}
		}
		metadata.Entries = append(metadata.Entries, entry)
	}
	encoded := jsonText(metadata)
	for len(encoded) > ideationRepositoryContextLimit/2 && len(metadata.Entries) > 0 {
		metadata.Entries = metadata.Entries[:len(metadata.Entries)-1]
		metadata.Complete = false
		encoded = jsonText(metadata)
	}
	return encoded
}

type ideationRepositoryIndex struct {
	Entries  []map[string]any `json:"entries"`
	Complete bool             `json:"complete"`
}

type ideationRepositorySource struct {
	Path     string `json:"path"`
	Revision string `json:"revision"`
	Content  string `json:"content,omitempty"`
	Complete bool   `json:"complete"`
}

func ideationRepositoryContextJSON(snapshot *discoverySnapshot, index ctxengine.Index, budget int) string {
	context := ideationRepositoryContext{IndexedPathsComplete: true, SourceCoverageComplete: true}
	if budget > ideationRepositoryContextLimit {
		budget = ideationRepositoryContextLimit
	}
	paths := ideationRepositorySourceOrder(*snapshot, index)
	for _, path := range paths {
		content, captured := snapshot.repositorySourceText[path]
		if !captured {
			context.SourceCoverageComplete = false
			continue
		}
		revision, evidenceExists := discoveryCodeEvidenceRevision(snapshot.Evidence, path)
		if !evidenceExists {
			revision, _ = discoveryEvidenceRevision(index, path, snapshot.Files[path])
			snapshot.Evidence = append(snapshot.Evidence, ctxengineEvidenceRef(path, revision))
		}
		candidate := ideationRepositorySource{Path: path, Revision: revision, Content: content, Complete: true}
		context.Sources = append(context.Sources, candidate)
		if len(jsonText(context)) > budget {
			context.Sources = context.Sources[:len(context.Sources)-1]
			context.SourceCoverageComplete = false
			if !evidenceExists {
				snapshot.Evidence = snapshot.Evidence[:len(snapshot.Evidence)-1]
			}
		}
	}
	for _, path := range sortedSnapshotPaths(snapshot.Files) {
		context.IndexedPaths = append(context.IndexedPaths, path)
		if len(jsonText(context)) > budget {
			context.IndexedPaths = context.IndexedPaths[:len(context.IndexedPaths)-1]
			context.IndexedPathsComplete = false
			break
		}
	}
	return jsonText(context)
}

func discoveryCodeEvidenceRevision(evidence []model.EvidenceRef, path string) (string, bool) {
	for _, ref := range evidence {
		if ref.Kind == "code" && ref.Ref == path {
			return ref.Revision, true
		}
	}
	return "", false
}

func ctxengineEvidenceRef(path, revision string) model.EvidenceRef {
	return model.EvidenceRef{Kind: "code", Ref: path, Revision: revision, Summary: "Current indexed source for ideation"}
}

func ideationRepositorySourceOrder(snapshot discoverySnapshot, index ctxengine.Index) []string {
	ordered := make([]string, 0, len(index.Files))
	seen := make(map[string]bool, len(index.Files))
	var sampledFallback []string
	for _, item := range snapshot.CurrentRepository {
		if path, ok := item["path"].(string); ok {
			ordered, sampledFallback = appendIdeationSourcePath(ordered, sampledFallback, seen, index, path)
		}
	}
	for _, path := range snapshot.ExplorationAreas {
		ordered, sampledFallback = appendIdeationSourcePath(ordered, sampledFallback, seen, index, path)
	}
	remaining := make([]string, 0, len(index.Files)-len(ordered))
	remainingFallback := make([]string, 0)
	for path := range index.Files {
		remaining, remainingFallback = appendRemainingIdeationPath(remaining, remainingFallback, seen, path)
	}
	sort.Strings(remaining)
	sort.Strings(remainingFallback)
	ordered = append(ordered, remaining...)
	ordered = append(ordered, sampledFallback...)
	return append(ordered, remainingFallback...)
}

func appendIdeationSourcePath(ordered, fallback []string, seen map[string]bool, index ctxengine.Index, path string) ([]string, []string) {
	if _, exists := index.Files[path]; !exists || seen[path] {
		return ordered, fallback
	}
	seen[path] = true
	if ctxengine.IsSourcePath(path) {
		return append(ordered, path), fallback
	}
	return ordered, append(fallback, path)
}

func appendRemainingIdeationPath(source, fallback []string, seen map[string]bool, path string) ([]string, []string) {
	if seen[path] {
		return source, fallback
	}
	if ctxengine.IsSourcePath(path) {
		return append(source, path), fallback
	}
	return source, append(fallback, path)
}
