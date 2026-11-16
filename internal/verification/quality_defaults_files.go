package verification

import (
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
)

func openDefaultQualityRoot(repository, destination string) (*os.Root, error) {
	source, err := filepath.EvalSymlinks(repository)
	if err != nil {
		return nil, err
	}
	canonical, err := proposedQualityDirectory(destination)
	if err != nil {
		return nil, err
	}
	if qualityPathsOverlap(source, canonical) {
		return nil, errors.New("external quality directory must be separate from source")
	}
	if err := os.MkdirAll(destination, 0700); err != nil {
		return nil, err
	}
	canonical, err = proposedQualityDirectory(destination)
	if err != nil {
		return nil, err
	}
	if qualityPathsOverlap(source, canonical) {
		return nil, errors.New("external quality directory changed into source")
	}
	return os.OpenRoot(canonical)
}

func qualityPathsOverlap(left, right string) bool {
	inside := func(parent, child string) bool {
		relative, err := filepath.Rel(parent, child)
		return err == nil && (relative == "." || filepath.IsLocal(relative))
	}
	return inside(left, right) || inside(right, left)
}

func proposedQualityDirectory(path string) (string, error) {
	path = filepath.Clean(path)
	cursor := path
	var suffix []string
	for {
		info, err := os.Lstat(cursor)
		if os.IsNotExist(err) {
			suffix = append([]string{filepath.Base(cursor)}, suffix...)
			cursor = filepath.Dir(cursor)
			continue
		}
		if err != nil {
			return "", err
		}
		if cursor == path && info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("external quality directory cannot be a link")
		}
		canonical, err := filepath.EvalSymlinks(cursor)
		if err != nil {
			return "", err
		}
		return filepath.Join(append([]string{canonical}, suffix...)...), nil
	}
}

func publishDefaultQualityFile(root *os.Root, path, content string) (string, error) {
	if err := root.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	temporary := filepath.Join(filepath.Dir(path), ".whirlwind-default-"+rand.Text())
	file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Remove(temporary) }()
	_, writeErr := file.WriteString(content)
	if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		return "", err
	}
	if err := root.Link(temporary, path); err != nil && !os.IsExist(err) {
		return "", err
	}
	if err := checkQualityTarget(root, path, true); err != nil {
		return "", err
	}
	retained, err := root.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = retained.Close() }()
	raw, err := io.ReadAll(io.LimitReader(retained, (1<<20)+1))
	return string(raw), err
}
