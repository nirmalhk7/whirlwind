package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
)

func codingSnapshot(ctx context.Context, cwd string) (string, string, string, error) {
	snapshot, production, tests, _, err := codingSnapshotManifest(ctx, cwd)
	return snapshot, production, tests, err
}

func codingSnapshotManifest(ctx context.Context, cwd string) (string, string, string, map[string]string, error) {
	files, err := github.Run(ctx, cwd, "git", "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return "", "", "", nil, err
	}
	var production, tests []string
	manifest := map[string]string{}
	names := strings.Split(files, "\x00")
	slices.Sort(names)
	for _, file := range names {
		if file == "" || strings.HasPrefix(file, ".git/") {
			continue
		}
		fingerprint, err := codingFileFingerprint(cwd, file)
		if err != nil {
			return "", "", "", nil, err
		}
		manifest[file] = fingerprint
		if fingerprint == "deleted" {
			continue
		}
		fingerprint = file + ":" + fingerprint
		if testPath(file) {
			tests = append(tests, fingerprint)
		} else {
			production = append(production, fingerprint)
		}
	}
	return evidenceDigest([]any{production, tests}), evidenceDigest(production), evidenceDigest(tests), manifest, nil
}

func codingFileFingerprint(cwd, file string) (string, error) {
	path := filepath.Join(cwd, file)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "deleted", nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("TDD snapshot requires regular file: %s", file)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	mode := "100644"
	if info.Mode()&0111 != 0 {
		mode = "100755"
	}
	return mode + ":" + model.Fingerprint(raw), nil
}

func testPath(path string) bool {
	testFile := strings.HasSuffix(path, "_test.go") || strings.Contains(path, ".test.") || strings.Contains(path, ".spec.")
	testDirectory := strings.HasPrefix(path, "tests/") || strings.Contains(path, "/tests/")
	return testFile || testDirectory || strings.HasPrefix(filepath.Base(path), "test_")
}

func productionPath(path string) bool {
	switch filepath.Ext(path) {
	case ".go", ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs", ".py", ".java", ".rs", ".rb", ".php", ".swift", ".kt", ".c", ".h", ".cpp", ".cs", ".sql", ".vue", ".svelte", ".sh", ".bash", ".zsh", ".ps1", ".html", ".css", ".scss", ".ex", ".exs", ".dart", ".scala", ".lua", ".pl", ".jl", ".m", ".mm", ".tf", ".nix":
		return true
	}
	return false
}

func tddFileValue(files map[string]string, file string) string {
	if value, ok := files[file]; ok {
		return value
	}
	return "deleted"
}

func tddChangedFiles(ctx context.Context, cwd, base, head string) ([]string, error) {
	args := []string{"diff", "--name-only", "--no-renames", "-z", base}
	if head != "" {
		args = append(args, head)
	}
	args = append(args, "--")
	files, err := github.Run(ctx, cwd, "git", args...)
	if err != nil {
		return nil, err
	}
	if head == "" {
		untracked, err := github.Run(ctx, cwd, "git", "ls-files", "--others", "--exclude-standard", "-z")
		if err != nil {
			return nil, err
		}
		files += "\x00" + untracked
	}
	return strings.Split(files, "\x00"), nil
}

func matchingTDDTests(tested, current map[string]string) bool {
	for file, value := range tested {
		if testPath(file) && value != tddFileValue(current, file) {
			return false
		}
	}
	return true
}
