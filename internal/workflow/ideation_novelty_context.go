package workflow

import (
	"sort"

	ctxengine "whirlwind/internal/context"
	"whirlwind/internal/model"
)

const noveltyRepositoryContextLimit = 24000

type noveltyRepositoryContext struct {
	IndexedTextPaths         []string                 `json:"indexed_text_paths"`
	IndexedTextPathsComplete bool                     `json:"indexed_text_paths_complete"`
	CandidatePaths           []noveltyCandidatePath   `json:"candidate_paths"`
	CandidatePathsComplete   bool                     `json:"candidate_paths_complete"`
	CandidateSources         []noveltyCandidateSource `json:"candidate_sources"`
	CandidateSourcesComplete bool                     `json:"candidate_sources_complete"`
	SampledRepository        []map[string]any         `json:"sampled_repository,omitempty"`
}

type noveltyCandidatePath struct {
	Path    string `json:"path"`
	Indexed bool   `json:"indexed"`
}

type noveltyCandidateSource struct {
	Path     string `json:"path"`
	Revision string `json:"revision"`
	Content  string `json:"content,omitempty"`
	Complete bool   `json:"complete"`
}

func frozenRepositorySourceText(index ctxengine.Index) map[string]string {
	sources := make(map[string]string, len(index.Files))
	for path, entry := range index.Files {
		sources[path] = entry.Text
	}
	return sources
}

func noveltyRepositoryContextJSON(snapshot discoverySnapshot, candidates []model.Direction) string {
	context := noveltyRepositoryContext{
		IndexedTextPaths:         sortedSnapshotPaths(snapshot.Files),
		IndexedTextPathsComplete: true,
		CandidatePathsComplete:   true,
		CandidateSourcesComplete: true,
		SampledRepository:        append([]map[string]any{}, snapshot.CurrentRepository...),
	}
	paths := candidateRepositoryPaths(candidates)
	for _, path := range paths {
		_, indexed := snapshot.Files[path]
		context.CandidatePaths = append(context.CandidatePaths, noveltyCandidatePath{Path: path, Indexed: indexed})
		if !indexed {
			continue
		}
		content, captured := snapshot.repositorySourceText[path]
		if !captured {
			context.CandidateSourcesComplete = false
			context.CandidateSources = append(context.CandidateSources, noveltyCandidateSource{Path: path, Revision: snapshot.Files[path]})
			continue
		}
		context.CandidateSources = append(context.CandidateSources, noveltyCandidateSource{Path: path, Revision: snapshot.Files[path], Content: content, Complete: true})
	}
	return boundedNoveltyRepositoryContext(context)
}

func candidateRepositoryPaths(candidates []model.Direction) []string {
	paths := map[string]bool{}
	for _, candidate := range candidates {
		for _, reference := range candidate.Evidence {
			if reference.Kind == "code" && reference.Ref != "" {
				paths[reference.Ref] = true
			}
		}
		for _, path := range candidate.Components {
			if path != "" {
				paths[path] = true
			}
		}
	}
	result := make([]string, 0, len(paths))
	for path := range paths {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

func boundedNoveltyRepositoryContext(context noveltyRepositoryContext) string {
	encoded := jsonText(context)
	for len(encoded) > noveltyRepositoryContextLimit && len(context.SampledRepository) > 0 {
		context.SampledRepository = context.SampledRepository[:len(context.SampledRepository)-1]
		encoded = jsonText(context)
	}
	for n := len(context.CandidateSources) - 1; len(encoded) > noveltyRepositoryContextLimit && n >= 0; n-- {
		context.CandidateSources[n].Content = ""
		context.CandidateSources[n].Complete = false
		context.CandidateSourcesComplete = false
		encoded = jsonText(context)
	}
	for len(encoded) > noveltyRepositoryContextLimit && len(context.CandidateSources) > 0 {
		context.CandidateSources = context.CandidateSources[:len(context.CandidateSources)-1]
		context.CandidateSourcesComplete = false
		encoded = jsonText(context)
	}
	for len(encoded) > noveltyRepositoryContextLimit && len(context.IndexedTextPaths) > 0 {
		context.IndexedTextPaths = context.IndexedTextPaths[:len(context.IndexedTextPaths)-1]
		context.IndexedTextPathsComplete = false
		encoded = jsonText(context)
	}
	for len(encoded) > noveltyRepositoryContextLimit && len(context.CandidatePaths) > 0 {
		context.CandidatePaths = context.CandidatePaths[:len(context.CandidatePaths)-1]
		context.CandidatePathsComplete = false
		encoded = jsonText(context)
	}
	return encoded
}

func sortedSnapshotPaths(files map[string]string) []string {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}
