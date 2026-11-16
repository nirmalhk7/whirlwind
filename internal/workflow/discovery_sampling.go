package workflow

import (
	"path/filepath"
	"slices"
	"sort"

	ctxengine "whirlwind/internal/context"
)

func rotatingDiscoveryAreas(paths []string, counts map[string]int, limit int) []string {
	if limit <= 0 || limit > 12 {
		limit = 12
	}
	areas := append([]string{}, paths...)
	sortExplorationPaths(areas, counts)
	selected := make([]string, 0, min(limit, len(areas)))
	seen := make(map[string]bool, len(areas))
	for _, path := range areas {
		if isDiscoveryGuidancePath(path) {
			selected = appendDiscoveryPath(selected, seen, path, limit)
		}
	}
	directorySources, sourcePaths := discoverySourceSamples(areas, counts)
	for _, path := range directorySources {
		selected = appendDiscoveryPath(selected, seen, path, limit)
	}
	for _, path := range sourcePaths {
		selected = appendDiscoveryPath(selected, seen, path, limit)
	}
	for _, path := range areas {
		selected = appendDiscoveryPath(selected, seen, path, limit)
	}
	return selected
}

func sortExplorationPaths(paths []string, counts map[string]int) {
	sort.SliceStable(paths, func(i, j int) bool {
		if counts[paths[i]] != counts[paths[j]] {
			return counts[paths[i]] < counts[paths[j]]
		}
		return paths[i] < paths[j]
	})
}

func appendDiscoveryPath(selected []string, seen map[string]bool, path string, limit int) []string {
	if seen[path] || len(selected) >= limit {
		return selected
	}
	seen[path] = true
	return append(selected, path)
}

func isDiscoveryGuidancePath(path string) bool {
	return filepath.Dir(path) == "." && slices.Contains([]string{"AGENTS.md", "CLAUDE.md", "PLAN.md", "PLANS.md"}, filepath.Base(path))
}

func discoverySourceSamples(paths []string, counts map[string]int) ([]string, []string) {
	byDirectory := map[string][]string{}
	sourcePaths := []string{}
	for _, path := range paths {
		if !ctxengine.IsSourcePath(path) {
			continue
		}
		sourcePaths = append(sourcePaths, path)
		byDirectory[filepath.Dir(path)] = append(byDirectory[filepath.Dir(path)], path)
	}
	directories := make([]string, 0, len(byDirectory))
	for directory := range byDirectory {
		directories = append(directories, directory)
	}
	sort.SliceStable(directories, func(i, j int) bool {
		left := leastExploredPath(byDirectory[directories[i]], counts)
		right := leastExploredPath(byDirectory[directories[j]], counts)
		if counts[left] != counts[right] {
			return counts[left] < counts[right]
		}
		return directories[i] < directories[j]
	})
	directorySources := make([]string, 0, len(directories))
	for _, directory := range directories {
		candidates := byDirectory[directory]
		sortExplorationPaths(candidates, counts)
		directorySources = append(directorySources, candidates[0])
	}
	sortExplorationPaths(sourcePaths, counts)
	return directorySources, sourcePaths
}

func leastExploredPath(paths []string, counts map[string]int) string {
	least := paths[0]
	for _, path := range paths[1:] {
		if counts[path] < counts[least] || counts[path] == counts[least] && path < least {
			least = path
		}
	}
	return least
}
