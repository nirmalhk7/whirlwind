// Package agents bundles the workflow agent instructions with the executable.
package agents

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"strings"
	"text/template"
)

//go:embed ideate.md ideate_novelty.md plan.md code.md review.md submit.md
var files embed.FS

var templates = template.Must(template.New("agents").ParseFS(files, "*.md"))

const MaxIdeaDescriptionWords = 35

// Render combines a named prompt with task context without interpreting or escaping that context.
func Render(name string, data map[string]string) (string, error) {
	if name == "ideate.md" {
		copy := make(map[string]string, len(data)+1)
		for key, value := range data {
			copy[key] = value
		}
		copy["MaxDescriptionWords"] = fmt.Sprint(MaxIdeaDescriptionWords)
		data = copy
	}
	var output bytes.Buffer
	if err := templates.ExecuteTemplate(&output, name, data); err != nil {
		return "", err
	}
	return strings.TrimSuffix(output.String(), "\n"), nil
}

// Source returns the trusted bundled Markdown before any repository or issue text is inserted.
func Source(name string) (string, error) {
	data, err := fs.ReadFile(files, name)
	if err != nil {
		return "", fmt.Errorf("read trusted agent prompt %q: %w", name, err)
	}
	return string(data), nil
}
