package workflow

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// nativeFixtureOrigin keeps the actual origin hosted while exercising local Git
// upload/receive protocols. The SSH shim never starts a network transport.
func nativeFixtureOrigin(t *testing.T, repo, canonical string) string {
	t.Helper()
	normalized, err := CanonicalRemote(canonical)
	if err != nil {
		t.Fatal(err)
	}
	remote, err := url.Parse(normalized)
	if err != nil {
		t.Fatal(err)
	}
	remote.Scheme, remote.User = "ssh", url.User("git")
	remote.Path += ".git"
	root := t.TempDir()
	bare := filepath.Join(root, "origin.git")
	script := filepath.Join(root, "local ssh")
	content := "#!/bin/sh\nfor argument do\n  case \"$argument\" in\n    git-upload-pack*) exec git-upload-pack " + nativeFixtureShellQuote(bare) + " ;;\n    git-receive-pack*) exec git-receive-pack " + nativeFixtureShellQuote(bare) + " ;;\n  esac\ndone\nexit 97\n"
	if err := os.WriteFile(script, []byte(content), 0700); err != nil {
		t.Fatal(err)
	}
	submissionGit(t, repo, "init", "--bare", bare)
	submissionGit(t, repo, "config", "core.sshCommand", nativeFixtureShellQuote(script))
	submissionGit(t, repo, "config", "ssh.variant", "simple")
	submissionGit(t, repo, "remote", "add", "origin", remote.String())
	submissionGit(t, repo, "push", "--set-upstream", "origin", "main")
	return bare
}

func nativeFixtureShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
