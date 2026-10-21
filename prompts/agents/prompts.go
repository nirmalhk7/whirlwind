// Package agents bundles the workflow agent instructions with the executable.
package agents

import (
	"bytes"
	"embed"
	"fmt"
	"strings"
	"text/template"
)

//go:embed ideate.md ideate_novelty.md plan.md code.md review.md learning.md
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
