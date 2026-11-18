package execution

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"whirlwind/internal/process"
)

const nativeGitQuiet = "--quiet"

// Dropping the shared Git read grant or granting its parent repository write
// access breaks this consumer-visible native launch, even if profile JSON looks valid.
func TestNonoLinkedWorktreeReadsSharedMetadataWithoutWriteAuthority(t *testing.T) {
	repo, worktree, source := nativeLinkedWorktreeFixture(t)
	profile, err := ForPolicy(worktree, false, "linked-native-acceptance", nil)
	if err != nil {
		t.Fatal(err)
	}
	profile.AllowedPaths = []string{"**"}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(repo, ".git", "config")
	before, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	script := `const fs=require('fs');if(!fs.readFileSync(process.argv[1],'utf8').includes('[core]'))throw new Error('shared metadata not readable');for(const path of [process.argv[1],process.argv[2]]){try{fs.appendFileSync(path,'escaped');throw new Error('shared write escaped')}catch(error){if(!['EPERM','EACCES'].includes(error.code))throw error}}fs.writeFileSync('accepted.txt','scoped');console.log('shared-read-scoped-write');`
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command, err := Command(ctx, []string{node, "-e", script, config, source}, profile)
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	command.Stdout = &output
	command.Stderr = &output
	if err := process.Run(command); err != nil {
		t.Fatalf("native linked-worktree launch: %v: %s", err, output.String())
	}
	if output.String() != "shared-read-scoped-write\n" {
		t.Fatalf("native controls failed: %s", output.String())
	}
	if after, err := os.ReadFile(config); err != nil || string(after) != string(before) {
		t.Fatalf("shared Git config changed: %v", err)
	}
	if after, err := os.ReadFile(source); err != nil || string(after) != "source" {
		t.Fatalf("primary worktree changed: %v", err)
	}
	if after, err := os.ReadFile(filepath.Join(worktree, "accepted.txt")); err != nil || string(after) != "scoped" {
		t.Fatalf("authorized scoped write missing: %v", err)
	}
}

func nativeLinkedWorktreeFixture(t *testing.T) (repo, worktree, source string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo, worktree = filepath.Join(root, "repo"), filepath.Join(root, "worktree")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("Git fixture: %v: %s", err, out)
		}
	}
	git("init", nativeGitQuiet, "--initial-branch=source")
	source = filepath.Join(repo, "source.txt")
	if err := os.WriteFile(source, []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", nativeGitQuiet, "-m", "Fixture")
	git("worktree", "add", nativeGitQuiet, "-b", "selected", worktree)
	return repo, worktree, source
}
