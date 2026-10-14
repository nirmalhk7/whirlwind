package context

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"maps"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"whirlwind/internal/model"
)

type Entry struct {
	Package      string    `json:"package,omitempty"`
	Dependencies []string  `json:"dependencies,omitempty"`
	Tests        []string  `json:"tests,omitempty"`
	Path         string    `json:"path"`
	Text         string    `json:"text"`
	Symbols      []string  `json:"symbols"`
	TODOs        []string  `json:"todos"`
	Vector       []float64 `json:"vector,omitempty"`
}
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
		for k, v := range previous.Files {
			idx.Files[k] = v
		}
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
		if strings.HasPrefix(p, ".git/") || strings.Contains(p, "node_modules/") {
			continue
		}
		b, e := exec.CommandContext(ctx, "git", "-C", cwd, "show", head+":"+p).Output()
		if e != nil {
			continue
		}
		if len(b) > 256*1024 || bytes.IndexByte(b, 0) >= 0 {
			continue
		}
		entry := Entry{Path: p, Text: string(b), Symbols: []string{}, TODOs: []string{}}
		for _, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, "TODO") || strings.Contains(line, "FIXME") {
				entry.TODOs = append(entry.TODOs, line)
			}
		}
		if filepath.Ext(p) == ".go" {
			f, e := parser.ParseFile(token.NewFileSet(), p, b, 0)
			if e == nil {
				entry.Package = f.Name.Name
				for _, spec := range f.Imports {
					dependency, x := strconv.Unquote(spec.Path.Value)
					if x == nil {
						entry.Dependencies = append(entry.Dependencies, dependency)
					}
				}
				ast.Inspect(f, func(n ast.Node) bool {
					switch x := n.(type) {
					case *ast.FuncDecl:
						entry.Symbols = append(entry.Symbols, x.Name.Name)
						if strings.HasPrefix(x.Name.Name, "Test") {
							entry.Tests = append(entry.Tests, x.Name.Name)
						}
					case *ast.TypeSpec:
						entry.Symbols = append(entry.Symbols, x.Name.Name)
					}
					return true
				})
			}
		} else {
			for _, line := range strings.Split(string(b), "\n") {
				s := strings.TrimSpace(line)
				if strings.HasPrefix(s, "export ") || strings.HasPrefix(s, "def ") || strings.HasPrefix(s, "class ") {
					if len(s) > 200 {
						s = s[:200]
					}
					entry.Symbols = append(entry.Symbols, s)
				}
			}
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
		if score > 0 {
			hits = append(hits, hit{e, score})
		}
	}
	sort.Slice(hits, func(a, b int) bool {
		if hits[a].score == hits[b].score {
			return hits[a].entry.Path < hits[b].entry.Path
		}
		return hits[a].score > hits[b].score
	})
	out := []Entry{}
	for n, h := range hits {
		if n >= limit {
			break
		}
		out = append(out, h.entry)
	}
	return out
}

// Package builds a bounded JSON context package with relevant files, nearby tests, and dependency manifests.
func (i Index) Package(query string) string {
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
		if (directories[filepath.Dir(key)] && len(e.Tests) > 0) || key == "go.mod" || key == "package.json" {
			entries = append(entries, e)
		}
	}
	for n := range entries {
		if len(entries[n].Text) > 12000 {
			entries[n].Text = entries[n].Text[:12000]
		}
	}
	for {
		b, _ := json.Marshal(map[string]any{"index_version": i.Version, "commit": i.Commit, "files": entries})
		if len(b) <= 256*1024 || len(entries) == 0 {
			return string(b)
		}
		entries = entries[:len(entries)-1]
	}
}

// Benefit requires better embedding recall with no loss of acceptance-criterion coverage.
func Benefit(baseline, embedded, baseCoverage, coverage float64) bool {
	return embedded > baseline && coverage >= baseCoverage
}

// Embed calls the configured embedding endpoint using an environment-held credential and validates returned vector indices and counts.
func Embed(ctx context.Context, c model.Embeddings, texts []string) ([][]float64, error) {
	body, _ := json.Marshal(map[string]any{"model": c.Model, "input": texts})
	req, e := http.NewRequestWithContext(ctx, "POST", c.Endpoint, bytes.NewReader(body))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Content-Type", "application/json")
	if c.KeyEnv != "" {
		key := os.Getenv(c.KeyEnv)
		if key == "" {
			return nil, errors.New("embedding credential environment variable missing")
		}
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := http.Client{Timeout: 60 * time.Second}
	resp, e := client.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("embedding response %d", resp.StatusCode)
	}
	var v struct {
		Data []struct {
			Index  int       `json:"index"`
			Vector []float64 `json:"embedding"`
		} `json:"data"`
	}
	e = json.NewDecoder(io.LimitReader(resp.Body, 16*1024*1024)).Decode(&v)
	if e != nil {
		return nil, e
	}
	if len(v.Data) != len(texts) {
		return nil, errors.New("embedding count mismatch")
	}
	vectors := make([][]float64, len(texts))
	for _, r := range v.Data {
		if r.Index < 0 || r.Index >= len(vectors) || len(r.Vector) == 0 || vectors[r.Index] != nil {
			return nil, errors.New("invalid embedding response")
		}
		vectors[r.Index] = r.Vector
	}
	return vectors, nil
}

// Cosine computes vector similarity, returning zero for empty, mismatched, or zero-length vectors.
func Cosine(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, aa, bb float64
	for n, x := range a {
		dot += x * b[n]
		aa += x * x
		bb += b[n] * b[n]
	}
	if aa == 0 || bb == 0 {
		return 0
	}
	return dot / math.Sqrt(aa*bb)
}

type Fixture struct {
	Query    string   `json:"query"`
	Relevant []string `json:"relevant"`
	Criteria []string `json:"criteria"`
}
type Evaluation struct {
	Baseline         float64 `json:"baseline"`
	Embedded         float64 `json:"embedded"`
	BaselineCoverage float64 `json:"baseline_coverage"`
	Coverage         float64 `json:"coverage"`
	Enabled          bool    `json:"enabled"`
}

// Evaluate embeds indexed files and fixture queries, compares retrieval recall and criterion coverage, and records whether vectors may be used.
func (i *Index) Evaluate(ctx context.Context, c model.Embeddings, fixtures []Fixture) (Evaluation, error) {
	result := Evaluation{}
	if len(fixtures) == 0 {
		return result, errors.New("retrieval fixtures required")
	}
	keys := slices.Sorted(maps.Keys(i.Files))
	texts := []string{}
	for _, k := range keys {
		e := i.Files[k]
		texts = append(texts, e.Path+"\n"+strings.Join(e.Symbols, " ")+"\n"+e.Text)
	}
	vectors, e := Embed(ctx, c, texts)
	if e != nil {
		return result, e
	}
	for n, k := range keys {
		x := i.Files[k]
		x.Vector = vectors[n]
		i.Files[k] = x
	}
	for _, f := range fixtures {
		q, e := Embed(ctx, c, []string{f.Query})
		if e != nil {
			return result, e
		}
		baseline := i.Search(f.Query, 5)
		embedded := i.VectorSearch(q[0], 5)
		score := func(entries []Entry) (float64, float64) {
			paths := map[string]bool{}
			combined := ""
			for _, e := range entries {
				paths[e.Path] = true
				combined += e.Text
			}
			hits := 0
			for _, p := range f.Relevant {
				if paths[p] {
					hits++
				}
			}
			criteria := 0
			for _, x := range f.Criteria {
				if strings.Contains(combined, x) {
					criteria++
				}
			}
			recall := 0.0
			if len(f.Relevant) > 0 {
				recall = float64(hits) / float64(len(f.Relevant))
			}
			coverage := 1.0
			if len(f.Criteria) > 0 {
				coverage = float64(criteria) / float64(len(f.Criteria))
			}
			return recall, coverage
		}
		b, bc := score(baseline)
		v, vc := score(embedded)
		result.Baseline += b
		result.Embedded += v
		result.BaselineCoverage += bc
		result.Coverage += vc
	}
	n := float64(len(fixtures))
	result.Baseline /= n
	result.Embedded /= n
	result.BaselineCoverage /= n
	result.Coverage /= n
	result.Enabled = Benefit(result.Baseline, result.Embedded, result.BaselineCoverage, result.Coverage)
	i.EmbeddingValidated = result.Enabled
	return result, nil
}

// VectorSearch ranks files with stored vectors by cosine similarity and returns at most limit entries.
func (i Index) VectorSearch(q []float64, limit int) []Entry {
	all := []Entry{}
	for _, e := range i.Files {
		if len(e.Vector) > 0 {
			all = append(all, e)
		}
	}
	sort.Slice(all, func(a, b int) bool { return Cosine(q, all[a].Vector) > Cosine(q, all[b].Vector) })
	if len(all) > limit {
		all = all[:limit]
	}
	return all
}

// Category selects the supported source extension with the most indexed text for comparable provider outcome routing.
func (i Index) Category() string {
	weights := map[string]int{}
	for path, entry := range i.Files {
		extension := filepath.Ext(path)
		switch extension {
		case ".go", ".py", ".ts", ".tsx", ".js", ".rs", ".java", ".c", ".cpp":
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
