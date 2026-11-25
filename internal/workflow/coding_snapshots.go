package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"whirlwind/internal/github"
)

func codingSnapshot(ctx context.Context, cwd string) (string, string, string, error) {
	snapshot, production, tests, _, err := codingSnapshotManifest(ctx, cwd)
	return snapshot, production, tests, err
}

func codingSnapshotManifest(ctx context.Context, cwd string) (string, string, string, map[string]string, error) {
	root, err := os.OpenRoot(cwd)
	if err != nil {
		return "", "", "", nil, err
	}
	defer func() { _ = root.Close() }()
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
		fingerprint, err := codingFileFingerprint(root, file)
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

func openCodingSnapshotFile(root *os.Root, path string) (*os.File, os.FileInfo, error) {
	info, err := root.Lstat(path)
	if os.IsNotExist(err) || errors.Is(err, syscall.ENOTDIR) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if info.IsDir() {
		return nil, nil, nil
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("TDD snapshot requires regular file: %s", path)
	}
	// Open relative to the pinned worktree. Nonblocking open and the descriptor
	// check also cover a file replaced between the metadata check and open.
	file, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	actual, err := file.Stat()
	if err != nil || !actual.Mode().IsRegular() || !os.SameFile(info, actual) {
		_ = file.Close()
		return nil, nil, fmt.Errorf("TDD snapshot file changed while opening: %s", path)
	}
	return file, actual, nil
}

func codingFileFingerprint(root *os.Root, path string) (string, error) {
	file, info, err := openCodingSnapshotFile(root, path)
	if err != nil {
		return "", err
	}
	if file == nil {
		return "deleted", nil
	}
	hash := sha256.New()
	_, readErr := io.Copy(hash, file)
	if err := errors.Join(readErr, file.Close()); err != nil {
		return "", err
	}
	mode := "100644"
	if info.Mode()&0111 != 0 {
		mode = "100755"
	}
	return mode + ":" + hex.EncodeToString(hash.Sum(nil)), nil
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
