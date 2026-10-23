package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func localPlanHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func (e *Engine) localPlanLocation(task model.Task) (string, string, error) {
	if !localPlanID(task.ProjectID) || !localPlanID(task.ID) || task.PlanVersion < 1 {
		return "", "", errors.New("local plans require safe task/project IDs and a positive version")
	}
	root := e.PlansRoot
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", err
		}
		root = filepath.Join(home, ".whirlwind", "plans")
	}
	root, err := filepath.Abs(root)
	return root, filepath.Join(task.ProjectID, task.ID, fmt.Sprintf("v%d.md", task.PlanVersion)), err
}

func localPlanID(id string) bool {
	return id != "" && safe(id) == id
}

func localPlanRoot(path string) (*os.Root, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("plans root must be a directory, not a symlink")
	}
	return os.OpenRoot(path)
}

func localPlanComponents(root *os.Root, relative string) error {
	current := ""
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := root.Lstat(current)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("local plan paths must not contain symlinks")
		}
	}
	return nil
}

func (e *Engine) readLocalPlanFile(task model.Task) (string, error) {
	rootPath, relative, err := e.localPlanLocation(task)
	if err != nil {
		return "", err
	}
	if task.PlanDocument == nil || task.PlanDocument.Path != filepath.Join(rootPath, relative) {
		return "", errors.New("stored plan path does not match the task document")
	}
	root, err := localPlanRoot(rootPath)
	if err != nil {
		return "", err
	}
	// Read-only descriptor cleanup cannot invalidate content already read.
	defer func() { _ = root.Close() }()
	return readBoundedLocalPlan(root, relative)
}

func readBoundedLocalPlan(root *os.Root, relative string) (string, error) {
	if err := localPlanComponents(root, relative); err != nil {
		return "", err
	}
	file, err := openLocalPlanFile(root, relative)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, maxLocalPlanBytes+1))
	if err != nil {
		return "", err
	}
	if len(content) > maxLocalPlanBytes {
		return "", errors.New("local plan exceeds 1 MiB")
	}
	return string(content), nil
}

func openLocalPlanFile(root *os.Root, relative string) (*os.File, error) {
	info, err := root.Lstat(relative)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxLocalPlanBytes {
		return nil, errors.New("local plan must be a regular file no larger than 1 MiB")
	}
	file, err := root.Open(relative)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		_ = file.Close()
		return nil, errors.New("local plan changed while opening")
	}
	return file, nil
}

func (e *Engine) writeLocalPlanFile(task model.Task, content string) (string, error) {
	if len(content) > maxLocalPlanBytes {
		return "", errors.New("local plan exceeds 1 MiB")
	}
	rootPath, relative, err := e.localPlanLocation(task)
	if err != nil {
		return "", err
	}
	root, err := writableLocalPlanRoot(rootPath, relative)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	full := filepath.Join(rootPath, relative)
	if err := publishLocalPlan(root, relative, content); err != nil {
		return "", err
	}
	return full, nil
}

func writableLocalPlanRoot(rootPath, relative string) (*os.Root, error) {
	if err := os.MkdirAll(rootPath, 0700); err != nil {
		return nil, err
	}
	root, err := localPlanRoot(rootPath)
	if err != nil {
		return nil, err
	}
	if err := localPlanComponents(root, relative); err != nil {
		_ = root.Close()
		return nil, err
	}
	if err := root.MkdirAll(filepath.Dir(relative), 0700); err != nil {
		_ = root.Close()
		return nil, err
	}
	return root, nil
}

func publishLocalPlan(root *os.Root, relative, content string) error {
	_, err := root.Lstat(relative)
	if err == nil {
		return matchLocalPlanContent(root, relative, content)
	}
	if !os.IsNotExist(err) {
		return err
	}
	return publishNewLocalPlan(root, relative, content)
}

func matchLocalPlanContent(root *os.Root, relative, content string) error {
	existing, err := readBoundedLocalPlan(root, relative)
	if err != nil || existing != content {
		return errors.New("existing local plan differs; refusing to overwrite edits")
	}
	return nil
}

func publishNewLocalPlan(root *os.Root, relative, content string) error {
	temporary := filepath.Join(filepath.Dir(relative), ".plan-"+store.ID())
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	// Failed writes and successful hard-link publication both leave this private
	// temporary name disposable. Cleanup must not replace the publication result.
	defer func() { _ = root.Remove(temporary) }()
	if err := writeLocalPlanContent(file, content); err != nil {
		return err
	}
	// Link publishes atomically without replacing a concurrently edited file.
	return root.Link(temporary, relative)
}

func writeLocalPlanContent(file *os.File, content string) error {
	_, err := io.WriteString(file, content)
	if err == nil {
		err = file.Sync()
	}
	return errors.Join(err, file.Close())
}
