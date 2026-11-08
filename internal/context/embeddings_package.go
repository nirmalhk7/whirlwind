package context

import (
	"encoding/json"
	"maps"
	"path/filepath"
	"slices"
	"unicode/utf8"
)

// Package builds a bounded JSON context package with relevant files, nearby tests, and dependency manifests.
func (i Index) Package(query string) string {
	entries := i.packageEntries(query)
	for n := range entries {
		entries[n].Vector = nil
		text := entries[n].Text[:min(len(entries[n].Text), 12000)]
		for !utf8.ValidString(text) && len(text) > 0 {
			text = text[:len(text)-1]
		}
		entries[n].Text = text
	}
	for {
		b, _ := json.Marshal(map[string]any{"index_version": i.Version, "commit": i.Commit, "files": entries})
		if len(b) <= 256*1024 || len(entries) == 0 {
			return string(b)
		}
		entries = entries[:len(entries)-1]
	}
}

func (i Index) packageEntries(query string) []Entry {
	entries := i.Search(query, 8)
	seen := map[string]bool{}
	directories := map[string]bool{}
	for _, e := range entries {
		seen[e.Path] = true
		directories[filepath.Dir(e.Path)] = true
	}
	keys := slices.Sorted(maps.Keys(i.Files))
	for _, key := range keys {
		e := i.Files[key]
		if seen[key] {
			continue
		}
		if (directories[filepath.Dir(key)] && len(e.Tests) > 0) || slices.Contains([]string{"go.mod", "package.json"}, key) {
			entries = append(entries, e)
		}
	}
	return entries
}
