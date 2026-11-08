package context

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

const DefaultInputAllowance = 32768

type PackRequest struct {
	Required       string
	Query          string
	Role           string
	PolicyHash     string
	MaxInputTokens int
	Additional     []Entry
}

type ContextPack struct {
	ID              string   `json:"id"`
	Commit          string   `json:"commit"`
	Role            string   `json:"role"`
	PolicyHash      string   `json:"policy_hash"`
	Prompt          string   `json:"-"`
	EstimatedTokens int      `json:"estimated_tokens"`
	CountMethod     string   `json:"count_method"`
	Included        []string `json:"included"`
	Omitted         []string `json:"omitted"`
}

type packPayload struct {
	Commit  string   `json:"commit"`
	Role    string   `json:"role"`
	Policy  string   `json:"policy_hash"`
	Files   []Entry  `json:"files"`
	Omitted []string `json:"omitted"`
}

// Pack reserves mandatory instructions first, then fits source excerpts into a
// conservative UTF-8-byte estimate. This is not a provider tokenizer guarantee.
func (i Index) Pack(request PackRequest) (ContextPack, error) {
	limit := request.MaxInputTokens
	if limit <= 0 {
		limit = DefaultInputAllowance
	}
	pack := ContextPack{Commit: i.Commit, Role: request.Role, PolicyHash: request.PolicyHash, CountMethod: "utf8_bytes_upper_estimate", Included: []string{}, Omitted: []string{}}
	selected := packPayload{i.Commit, request.Role, request.PolicyHash, []Entry{}, []string{}}
	render := func() string {
		raw, _ := json.Marshal(selected)
		return request.Required + "\nSelected repository context (excerpts; read referenced files for omitted content):\n" + string(raw)
	}
	if len(render()) > limit {
		return pack, errors.New("required instructions exceed context input allowance; increase context_input_tokens or reduce task scope")
	}
	entries := packEntries(i, request)
	seen := map[string]bool{}
	originalText := map[string]string{}
	for _, entry := range entries {
		if seen[entry.Path] {
			continue
		}
		seen[entry.Path] = true
		originalText[entry.Path] = entry.Text
		entry.Vector = nil
		entry.Text = utf8Prefix(entry.Text, 12000)
		// Bound redundant index metadata as well as source text.
		entry.TODOs = nil
		entry.ActionMarkers = nil
		entry.Symbols = nil
		entry.Dependencies = nil
		original := i.Files[entry.Path].Text
		if original == "" {
			original = entry.Text
		}
		if fitPackEntry(&selected, entry, len(original), limit, render) {
			pack.Included = append(pack.Included, entry.Path)
		}
	}
	pack.Prompt = render()
	pack.EstimatedTokens = len(pack.Prompt)
	// Stored provenance is complete even when its full path list cannot fit the prompt.
	retained := map[string]string{}
	for _, entry := range selected.Files {
		retained[entry.Path] = entry.Text
	}
	for path, original := range originalText {
		text, present := retained[path]
		if !present || text != original {
			pack.Omitted = append(pack.Omitted, path)
		}
	}
	slices.Sort(pack.Omitted)
	sum := sha256.Sum256([]byte(pack.Prompt))
	pack.ID = hex.EncodeToString(sum[:])
	return pack, nil
}

func packEntries(index Index, request PackRequest) []Entry {
	entries := append(index.Search(request.Query, 12), request.Additional...)
	dirs := map[string]bool{}
	for _, entry := range entries {
		dirs[filepath.Dir(entry.Path)] = true
	}
	keys := make([]string, 0, len(index.Files))
	for key := range index.Files {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		entry := index.Files[key]
		if (dirs[filepath.Dir(key)] && len(entry.Tests) > 0) || slices.Contains([]string{"go.mod", "package.json"}, key) {
			entries = append(entries, entry)
		}
	}
	return entries
}

func fitPackEntry(selected *packPayload, entry Entry, originalLength, limit int, render func() string) bool {
	selected.Files = append(selected.Files, entry)
	index := len(selected.Files) - 1
	if len(render()) > limit {
		return fitTruncatedPackEntry(selected, entry, index, limit, render)
	}
	if len(entry.Text) < originalLength {
		selected.Omitted = append(selected.Omitted, entry.Path)
		if len(render()) > limit {
			selected.Omitted = selected.Omitted[:len(selected.Omitted)-1]
			selected.Files = selected.Files[:index]
			return false
		}
	}
	return true
}

func fitTruncatedPackEntry(selected *packPayload, entry Entry, index, limit int, render func() string) bool {
	// Mark truncation before fitting so reference metadata is included in budget.
	selected.Omitted = append(selected.Omitted, entry.Path)
	lo, hi := 0, len(entry.Text)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		selected.Files[index].Text = utf8Prefix(entry.Text, mid)
		if len(render()) <= limit {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	selected.Files[index].Text = utf8Prefix(entry.Text, lo)
	if lo == 0 || len(render()) > limit {
		selected.Files = selected.Files[:index]
		if len(render()) > limit {
			selected.Omitted = selected.Omitted[:len(selected.Omitted)-1]
		}
		return false
	}
	return true
}

func utf8Prefix(value string, limit int) string {
	value = value[:min(len(value), max(0, limit))]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return strings.ToValidUTF8(value, "")
}
