package dashboard

import (
	"os"
	"path/filepath"
	"testing"
)

func markdownVim(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	capture := filepath.Join(directory, "original.md")
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MARKDOWN_CAPTURE", capture)
	script := `#!/usr/bin/env python3
import os,sys
p=sys.argv[-1]
s=open(p).read()
open(os.environ['MARKDOWN_CAPTURE'],'w').write(s)
open(p,'w').write(s+'\n\nEdited implementation detail.\n')
`
	if err := os.WriteFile(filepath.Join(directory, "vim"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return capture
}
