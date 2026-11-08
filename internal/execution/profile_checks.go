package execution

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ForCheck grants declared build directories without making source or Git metadata writable.
func ForCheck(p Profile, outputs []string) (Profile, error) {
	p.ReadOnly = true
	p.WriteRoots = nil
	for _, path := range outputs {
		clean, err := cleanCheckOutputPath(path)
		if err != nil {
			return p, err
		}
		target := filepath.Join(p.CWD, clean)
		if err := validateCheckOutputTarget(p.CWD, target); err != nil {
			return p, err
		}
		if err := prepareCheckOutputDirectory(p.CWD, target); err != nil {
			return p, err
		}
		p.WriteRoots = append(p.WriteRoots, target)
	}
	return p, nil
}

func prepareCheckOutputDirectory(cwd, target string) error {
	if err := os.MkdirAll(target, 0700); err != nil {
		return fmt.Errorf("prepare declared check output directory: %w", err)
	}
	return validateCheckOutputTarget(cwd, target)
}

func cleanCheckOutputPath(path string) (string, error) {
	clean := filepath.Clean(path)
	if unsafeCheckOutputPath(clean) {
		return "", fmt.Errorf("unsafe check output path: %s", path)
	}
	return clean, nil
}

func unsafeCheckOutputPath(path string) bool {
	return !filepath.IsLocal(path) || unsafeCheckOutputName(path)
}

func unsafeCheckOutputName(path string) bool {
	return path == "." || path == ".git" || unsafeCheckOutputPrefix(path)
}

func unsafeCheckOutputPrefix(path string) bool {
	return strings.HasPrefix(path, ".git/") || strings.HasPrefix(path, ".whirlwind.")
}

func validateCheckOutputTarget(cwd, target string) error {
	if err := validateCheckOutputTree(target); err != nil {
		return err
	}
	return validateCheckOutputContainment(cwd, target)
}

func validateCheckOutputTree(target string) error {
	return filepath.WalkDir(target, func(path string, entry os.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			return errors.New("check outputs must be fresh directories, not existing source files or links")
		}
		return nil
	})
}

func validateCheckOutputContainment(cwd, target string) error {
	prefix := target
	for {
		resolved, err := filepath.EvalSymlinks(prefix)
		if err == nil {
			rel, e := filepath.Rel(cwd, resolved)
			if e != nil || rel == ".." || strings.HasPrefix(rel, "../") {
				return errors.New("check output symlink escapes snapshot")
			}
			return nil
		}
		if !os.IsNotExist(err) {
			return err
		}
		parent := filepath.Dir(prefix)
		if parent == prefix {
			return errors.New("check output cannot be resolved")
		}
		prefix = parent
	}
}
