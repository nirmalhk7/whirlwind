package architecture

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// These are production package boundaries, not a copy of every dependency.
// Test fixtures may import higher layers; production files on every OS may not.
func TestPackageBoundaries(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate repository")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	allowed := map[string][]string{
		"internal/context":         {"internal/model"},
		"internal/github":          {"internal/model", "internal/process"},
		"internal/evaluation":      {"internal/model", "internal/store"},
		"internal/provider":        {"internal/execution", "internal/model", "internal/policy", "internal/process", "internal/store"},
		"internal/workflow":        {"internal/context", "internal/execution", "internal/github", "internal/model", "internal/policy", "internal/process", "internal/provider", "internal/store", "internal/verification", "prompts/agents"},
		"internal/projectregistry": {"internal/github", "internal/model", "internal/policy", "internal/workflow"},
		"internal/settingsfile":    {"internal/model", "internal/projectregistry", "internal/provider", "internal/store", "internal/workflow"},
		"internal/model":           {},
		"internal/process":         {},
		"internal/redaction":       {},
		"internal/policy":          {"internal/model"},
		"internal/runlog":          {"internal/model"},
		"internal/store":           {"internal/model", "internal/redaction", "internal/runlog"},
		"internal/control":         {"internal/model", "internal/store"},
		"internal/dashboard":       {"internal/control", "internal/model", "internal/store"},
		"cmd/whirlwind-dashboard":  {"internal/control", "internal/dashboard", "internal/model", "internal/store"},
		"internal/execution":       {"internal/model", "internal/process", "internal/redaction"},
		"internal/verification":    {"internal/execution", "internal/model", "internal/policy", "internal/process"},
	}
	for pkg, dependencies := range allowed {
		t.Run(pkg, func(t *testing.T) {
			entries, err := os.ReadDir(filepath.Join(root, pkg))
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
					continue
				}
				path := filepath.Join(root, pkg, entry.Name())
				parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
				if err != nil {
					t.Fatal(err)
				}
				for _, spec := range parsed.Imports {
					dependency, err := strconv.Unquote(spec.Path.Value)
					if err != nil {
						t.Fatal(err)
					}
					if !strings.HasPrefix(dependency, "whirlwind/") {
						continue
					}
					dependency = strings.TrimPrefix(dependency, "whirlwind/")
					if !slices.Contains(dependencies, dependency) {
						t.Errorf("%s imports %s outside its package boundary", path, dependency)
					}
				}
			}
		})
	}
}
