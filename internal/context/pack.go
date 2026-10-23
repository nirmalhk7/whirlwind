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

// Pack reserves mandatory instructions first, then fits source excerpts into a
// conservative UTF-8-byte estimate. This is not a provider tokenizer guarantee.
func (i Index) Pack(request PackRequest) (ContextPack, error) {
	limit := request.MaxInputTokens
	if limit <= 0 {
		limit = DefaultInputAllowance
	}
	pack := ContextPack{Commit: i.Commit, Role: request.Role, PolicyHash: request.PolicyHash, CountMethod: "utf8_bytes_upper_estimate", Included: []string{}, Omitted: []string{}}
	type payload struct {
		Commit  string   `json:"commit"`
		Role    string   `json:"role"`
		Policy  string   `json:"policy_hash"`
		Files   []Entry  `json:"files"`
		Omitted []string `json:"omitted"`
	}
	selected := payload{i.Commit, request.Role, request.PolicyHash, []Entry{}, []string{}}
	render := func() string {
		raw, _ := json.Marshal(selected)
		return request.Required + "\nSelected repository context (excerpts; read referenced files for omitted content):\n" + string(raw)
	}
	if len(render()) > limit {
		return pack, errors.New("required instructions exceed context input allowance; increase context_input_tokens or reduce task scope")
	}
	entries := i.Search(request.Query, 12)
	entries = append(entries, request.Additional...)
	dirs := map[string]bool{}
	for _, entry := range entries {
		dirs[filepath.Dir(entry.Path)] = true
	}
	keys := make([]string, 0, len(i.Files))
	for key := range i.Files {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		entry := i.Files[key]
		if (dirs[filepath.Dir(key)] && len(entry.Tests) > 0) || slices.Contains([]string{"go.mod", "package.json"}, key) {
			entries = append(entries, entry)
		}
	}
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
		selected.Files = append(selected.Files, entry)
		n := len(selected.Files) - 1
		if len(render()) > limit {
			// Mark truncation before fitting so reference metadata is included in budget.
			selected.Omitted = append(selected.Omitted, entry.Path)
			lo, hi := 0, len(entry.Text)
			for lo < hi {
				mid := (lo + hi + 1) / 2
				selected.Files[n].Text = utf8Prefix(entry.Text, mid)
				if len(render()) <= limit {
					lo = mid
				} else {
					hi = mid - 1
				}
			}
			selected.Files[n].Text = utf8Prefix(entry.Text, lo)
			if lo == 0 || len(render()) > limit {
				selected.Files = selected.Files[:n]
				if len(render()) > limit {
					selected.Omitted = selected.Omitted[:len(selected.Omitted)-1]
				}
				continue
			}
		} else if len(entry.Text) < len(original) {
			selected.Omitted = append(selected.Omitted, entry.Path)
			if len(render()) > limit {
				selected.Omitted = selected.Omitted[:len(selected.Omitted)-1]
				selected.Files = selected.Files[:n]
				continue
			}
		}
		pack.Included = append(pack.Included, entry.Path)
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

func utf8Prefix(value string, limit int) string {
	value = value[:min(len(value), max(0, limit))]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return strings.ToValidUTF8(value, "")
}
