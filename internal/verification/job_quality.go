package verification

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (r *jobRunner) prepareQualityConfiguration(stage string) error {
	if r.manifest.Quality == nil {
		return nil
	}
	root, err := os.OpenRoot(stage)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	for _, path := range r.qualityPaths() {
		if err := checkQualityTarget(root, path, false); err != nil {
			return err
		}
		if err := root.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		file, err := root.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, writeErr := file.WriteString(r.manifest.Quality.Files[path])
		if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
			return err
		}
	}
	return nil
}

func (r *jobRunner) qualityPaths() []string {
	var paths []string
	if r.manifest.Quality != nil {
		for path := range r.manifest.Quality.Files {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths
}

func checkQualityTarget(root *os.Root, path string, required bool) error {
	prefix := ""
	components := strings.Split(path, string(filepath.Separator))
	for i, component := range components {
		prefix = filepath.Join(prefix, component)
		info, err := root.Lstat(prefix)
		if os.IsNotExist(err) && !required {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || i < len(components)-1 && !info.IsDir() || i == len(components)-1 && !info.Mode().IsRegular() {
			return errors.New("external quality configuration cannot replace links or nonregular paths")
		}
	}
	return nil
}

func (r *jobRunner) validateFrozenCheckout(ctx context.Context, stage string) error {
	if err := r.validateQualityConfiguration(stage); err != nil {
		return err
	}
	paths := r.qualityPaths()
	args := []string{"diff", "--exit-code", "HEAD", "--", "."}
	for _, path := range paths {
		args = append(args, ":(exclude,literal)"+path)
	}
	if _, err := r.git(ctx, stage, args...); err != nil {
		return errors.New("verification checkout source changed")
	}
	return nil
}

func (r *jobRunner) validateQualityConfiguration(stage string) error {
	paths := r.qualityPaths()
	if len(paths) == 0 {
		return nil
	}
	root, err := os.OpenRoot(stage)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	for _, path := range paths {
		if err := checkQualityTarget(root, path, true); err != nil {
			return err
		}
		file, err := root.Open(path)
		if err != nil {
			return err
		}
		expected := r.manifest.Quality.Files[path]
		content, readErr := io.ReadAll(io.LimitReader(file, int64(len(expected)+1)))
		if err := errors.Join(readErr, file.Close()); err != nil {
			return err
		}
		if string(content) != expected {
			return fmt.Errorf("approved external quality configuration changed in staging: %s", path)
		}
	}
	return nil
}
