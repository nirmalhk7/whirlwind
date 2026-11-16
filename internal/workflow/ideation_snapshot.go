package workflow

import (
	"sort"

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
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}
