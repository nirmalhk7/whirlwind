package context

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

func sourceEntry(p string, b []byte) Entry {
	entry := Entry{Path: p, Text: string(b), Symbols: []string{}, TODOs: []string{}}
	for n, line := range strings.Split(string(b), "\n") {
		normalized := strings.ToLower(line)
		if strings.Contains(normalized, "todo") || strings.Contains(normalized, "fixme") {
			entry.TODOs = append(entry.TODOs, line)
			tag := todoMarker
			if strings.Contains(normalized, "fixme") {
				tag = fixmeMarker
			}
			entry.ActionMarkers = append(entry.ActionMarkers, ActionMarker{Path: p, Line: n + 1, Tag: tag, Text: line})
		}
	}
	if filepath.Ext(p) == ".go" {
		entry.indexGo(p, b)
	} else {
		entry.indexText(b)
	}
	return entry
}

func (entry *Entry) indexGo(path string, source []byte) {
	f, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		return
	}
	entry.Package = f.Name.Name
	for _, spec := range f.Imports {
		if dependency, err := strconv.Unquote(spec.Path.Value); err == nil {
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

func readSourceEntry(ctx context.Context, cwd, head, p string) (Entry, bool) {
	if strings.HasPrefix(p, ".git/") || strings.Contains(p, "node_modules/") {
		return Entry{}, false
	}
	b, e := exec.CommandContext(ctx, "git", "-C", cwd, "show", head+":"+p).Output()
	if e != nil {
		return Entry{}, false
	}
	if len(b) > 256*1024 || bytes.IndexByte(b, 0) >= 0 {
		return Entry{}, false
	}
	return sourceEntry(p, b), true
}

var textSymbolPrefix = regexp.MustCompile(`^(export |def |class )`)

func (entry *Entry) indexText(b []byte) {

	for _, line := range strings.Split(string(b), "\n") {
		s := strings.TrimSpace(line)
		if textSymbolPrefix.MatchString(s) {
			s = s[:min(len(s), 200)]
			entry.Symbols = append(entry.Symbols, s)
		}
	}
}
