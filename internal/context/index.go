package context

import (
	"cmp"
	"context"
	"maps"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

type ActionMarker struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Tag  string `json:"tag"`
	Text string `json:"text"`
}
type Entry struct {
	ActionMarkers []ActionMarker `json:"action_markers,omitempty"`
	Package       string         `json:"package,omitempty"`
	Dependencies  []string       `json:"dependencies,omitempty"`
	Tests         []string       `json:"tests,omitempty"`
	Path          string         `json:"path"`
	Text          string         `json:"text"`
	Symbols       []string       `json:"symbols"`
	TODOs         []string       `json:"todos"`
	Vector        []float64      `json:"vector,omitempty"`
}

const (
	todoMarker  = "TO" + "DO"
	fixmeMarker = "FIX" + "ME"
)

type Index struct {
	Commit             string           `json:"commit"`
	Version            int              `json:"version"`
	Files              map[string]Entry `json:"files"`
	EmbeddingValidated bool             `json:"embedding_validated"`
}

// git runs a read-only Git query in the repository and trims its output.
func git(ctx context.Context, cwd string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, "git", append([]string{"-C", cwd}, args...)...)
	b, e := c.Output()
	return strings.TrimSpace(string(b)), e
}

// Build indexes text and symbols at a Git revision, updating only changed paths and invalidating embedding approval when the commit changes.
func Build(ctx context.Context, cwd string, previous *Index, revision ...string) (Index, error) {
	ref := "HEAD"
	if len(revision) > 0 {
		ref = revision[0]
	}
	head, e := git(ctx, cwd, "rev-parse", ref)
	if e != nil {
		return Index{}, e
	}
	idx := Index{Commit: head, Version: 1, Files: map[string]Entry{}}
	var paths string
	if previous != nil {
		idx.Version = previous.Version
		maps.Copy(idx.Files, previous.Files)
		idx.EmbeddingValidated = previous.EmbeddingValidated
		if previous.Commit == head {
			return idx, nil
		}
		idx.Version++
		idx.EmbeddingValidated = false
		paths, e = git(ctx, cwd, "diff", "--name-only", previous.Commit, head)
	} else {
		paths, e = git(ctx, cwd, "ls-tree", "-r", "--name-only", head)
	}
	if e != nil {
		return idx, e
	}
	for _, p := range strings.Split(paths, "\n") {
		if p == "" {
			continue
		}
		delete(idx.Files, p)
		entry, ok := readSourceEntry(ctx, cwd, head, p)
		if !ok {
			continue
		}
		idx.Files[p] = entry
	}
	return idx, nil
}

// Search ranks files by query matches in paths, symbols, and source text, using path order to break ties.
func (i Index) Search(query string, limit int) []Entry {
	type hit struct {
		entry Entry
		score float64
	}
	hits := []hit{}
	words := strings.Fields(strings.ToLower(query))
	for _, e := range i.Files {
		score := e.searchScore(words)
		if score > 0 {
			hits = append(hits, hit{e, score})
		}
	}
	sort.Slice(hits, func(a, b int) bool {
		return cmp.Or(
			cmp.Compare(hits[b].score, hits[a].score),
			strings.Compare(hits[a].entry.Path, hits[b].entry.Path),
		) < 0
	})
	out := []Entry{}
	for _, h := range hits[:min(max(limit, 0), len(hits))] {
		out = append(out, h.entry)
	}
	return out
}

// Category selects the supported source extension with the most indexed text for comparable provider outcome routing.
func (i Index) Category() string {
	weights := map[string]int{}
	for path, entry := range i.Files {
		extension := filepath.Ext(path)
		if slices.Contains([]string{".go", ".py", ".ts", ".tsx", ".js", ".rs", ".java", ".c", ".cpp"}, extension) {
			weights[extension] += len(entry.Text)
		}
	}
	keys := slices.Sorted(maps.Keys(weights))
	category := "other"
	best := 0
	for _, key := range keys {
		if weights[key] > best {
			best = weights[key]
			category = key
		}
	}
	return category
}

func (e Entry) searchScore(words []string) float64 {
	score := 0.0
	hay := strings.ToLower(e.Path + " " + strings.Join(e.Symbols, " "))
	text := strings.ToLower(e.Text)
	for _, w := range words {
		if len(w) < 3 {
			continue
		}
		if strings.Contains(hay, w) {
			score += 5
		}
		score += float64(strings.Count(text, w))
	}
	return score
}
