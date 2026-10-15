package context

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"whirlwind/internal/model"
)

// Benefit requires better embedding recall with no loss of acceptance-criterion coverage.
func Benefit(baseline, embedded, baseCoverage, coverage float64) bool {
	return embedded > baseline && coverage >= baseCoverage
}

// Embed calls the configured embedding endpoint using an environment-held credential and validates returned vector indices and counts.
func Embed(ctx context.Context, c model.Embeddings, texts []string) ([][]float64, error) {
	req, e := embeddingRequest(ctx, c, texts)
	if e != nil {
		return nil, e
	}
	client := http.Client{Timeout: 60 * time.Second}
	resp, e := client.Do(req)
	if e != nil {
		return nil, e
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("embedding response %d", resp.StatusCode)
	}
	return decodeEmbeddings(resp.Body, len(texts))
}

// Cosine computes vector similarity, returning zero for empty, mismatched, or zero-length vectors.
func Cosine(a, b []float64) float64 {
	if len(a) != len(b) {
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
		b, bc := f.score(baseline)
		v, vc := f.score(embedded)
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

func (f Fixture) score(entries []Entry) (float64, float64) {
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
	recall := float64(hits) / float64(max(len(f.Relevant), 1))
	coverage := 1.0
	if len(f.Criteria) > 0 {
		coverage = float64(criteria) / float64(len(f.Criteria))
	}
	return recall, coverage
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
		if (directories[filepath.Dir(key)] && len(e.Tests) > 0) || slices.Contains([]string{"go.mod", "package.json"}, key) {
			entries = append(entries, e)
		}
	}
	for n := range entries {
		entries[n].Text = entries[n].Text[:min(len(entries[n].Text), 12000)]
	}
	for {
		b, _ := json.Marshal(map[string]any{"index_version": i.Version, "commit": i.Commit, "files": entries})
		if len(b) <= 256*1024 || len(entries) == 0 {
			return string(b)
		}
		entries = entries[:len(entries)-1]
	}
}

func embeddingRequest(ctx context.Context, c model.Embeddings, texts []string) (*http.Request, error) {
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
	return req, nil
}

func decodeEmbeddings(body io.Reader, count int) ([][]float64, error) {
	var v struct {
		Data []struct {
			Index  int       `json:"index"`
			Vector []float64 `json:"embedding"`
		} `json:"data"`
	}
	e := json.NewDecoder(io.LimitReader(body, 16*1024*1024)).Decode(&v)
	if e != nil {
		return nil, e
	}
	if len(v.Data) != count {
		return nil, errors.New("embedding count mismatch")
	}
	vectors := make([][]float64, count)
	for _, r := range v.Data {
		if uint(r.Index) >= uint(len(vectors)) || len(r.Vector) == 0 || vectors[r.Index] != nil {
			return nil, errors.New("invalid embedding response")
		}
		vectors[r.Index] = r.Vector
	}
	return vectors, nil
}
