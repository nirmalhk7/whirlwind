package dashboard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanDecisionEnablesMarkdownSyntaxInRealVim(t *testing.T) {
	realVim, err := exec.LookPath("vim")
	if err != nil {
		t.Skip("Vim is not installed")
	}
	directory := t.TempDir()
	result := filepath.Join(directory, "syntax-result")
	probe := filepath.Join(directory, "probe.vim")
	// A clean Vim starts without file detection or highlighting. Probe the live
	// buffer after the dashboard's startup commands, rather than matching argv.
	script := "call writefile([&filetype, &syntax, string(exists('g:syntax_on')), synIDattr(synID(1, 3, 1), 'name')], $VIM_SYNTAX_RESULT)\nqa!\n"
	if err := os.WriteFile(probe, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	wrapper := `#!/usr/bin/env python3
import os,sys
args=[arg for arg in sys.argv[1:] if arg!='--']
os.execv(os.environ['REAL_REVIEW_VIM'],[os.environ['REAL_REVIEW_VIM'],'-Nu','NONE','-i','NONE','-es']+args+['-S',os.environ['VIM_SYNTAX_PROBE']])
`
	if err := os.WriteFile(filepath.Join(directory, "vim"), []byte(wrapper), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REAL_REVIEW_VIM", realVim)
	t.Setenv("VIM_SYNTAX_RESULT", result)
	t.Setenv("VIM_SYNTAX_PROBE", probe)
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	d := New()
	dashboardScreen(t, d)
	d.editDecision("# Detailed execution plan\n\n## Acceptance criteria\n- Preserve existing behavior.\n", []string{"approve", "revise", "reject"})
	body, err := os.ReadFile(result)
	if err != nil {
		t.Fatalf("Vim syntax probe failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) != 4 || lines[0] != "markdown" || lines[1] != "markdown" || lines[2] != "1" || lines[3] == "" {
		t.Fatalf("Markdown highlighting not active in review buffer: %q", body)
	}
}
