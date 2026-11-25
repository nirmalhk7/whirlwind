package workflow

import (
	"maps"
	"slices"

	ctxengine "whirlwind/internal/context"
)

func frozenRepositorySourceText(index ctxengine.Index) map[string]string {
	sources := make(map[string]string, len(index.Files))
	for path, entry := range index.Files {
		sources[path] = entry.Text
	}
	return sources
}

func sortedSnapshotPaths(files map[string]string) []string {
	paths := slices.AppendSeq(make([]string, 0, len(files)), maps.Keys(files))
	slices.Sort(paths)
	return paths
}
