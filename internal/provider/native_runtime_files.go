package provider

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func nativeUserHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(home) || filepath.Clean(home) == string(filepath.Separator) {
		return "", errors.New("native integration home must be bounded and absolute")
	}
	return filepath.EvalSymlinks(home)
}

// nativeRegularFile rejects source symlinks, including intermediate directories.
// Resolving the bounded root first permits the operating system's /var alias.
func nativeRegularFile(root, relative string) (string, bool, error) {
	if !filepath.IsLocal(relative) || relative == "." {
		return "", false, errors.New("native integration file must be relative to its bounded root")
	}
	path := root
	parts := strings.Split(filepath.Clean(relative), string(filepath.Separator))
	for i, part := range parts {
		path = filepath.Join(path, part)
		exists, err := checkNativeFileEntry(path, i == len(parts)-1)
		if err != nil || !exists {
			return "", false, err
		}
	}
	return path, true, nil
}

func checkNativeFileEntry(path string, final bool) (bool, error) {
	entry, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("native integration file metadata unavailable: %w", err)
	}
	if !nativeFileEntryAllowed(entry, final) {
		return false, errors.New("native integration configuration must use regular files without symlinks")
	}
	if final && entry.Size() > 8*1024*1024 {
		return false, errors.New("native integration configuration exceeds bounded file size")
	}
	return true, nil
}

func nativeFileEntryAllowed(entry os.FileInfo, final bool) bool {
	if entry.Mode()&os.ModeSymlink != 0 {
		return false
	}
	if final {
		return entry.Mode().IsRegular()
	}
	return entry.IsDir()
}

func provisionCodexPluginManifests(home string, add func(string, string, string) error) error {
	exists, err := nativePluginCacheExists(home)
	if err != nil || !exists {
		return err
	}
	inventory := nativePluginInventory{cache: filepath.Join(home, ".codex", "plugins", "cache"), add: add}
	return filepath.WalkDir(inventory.cache, inventory.visit)
}

func nativePluginCacheExists(home string) (bool, error) {
	// Check every existing parent before walking; WalkDir itself does not follow
	// directory symlinks, but its starting path can otherwise traverse one.
	for _, relative := range []string{".codex", ".codex/plugins", ".codex/plugins/cache"} {
		entry, err := os.Lstat(filepath.Join(home, relative))
		if os.IsNotExist(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if entry.Mode()&os.ModeSymlink != 0 || !entry.IsDir() {
			return false, errors.New("native plugin cache must be a regular bounded directory")
		}
	}
	return true, nil
}

type nativePluginInventory struct {
	cache string
	count int
	add   func(string, string, string) error
}

func (inventory *nativePluginInventory) visit(path string, entry fs.DirEntry, err error) error {
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(inventory.cache, path)
	if err != nil {
		return err
	}
	depth := strings.Count(filepath.ToSlash(relative), "/")
	if entry.IsDir() {
		return nativePluginDirectory(entry.Name(), depth)
	}
	if !nativePluginManifest(path, entry.Name(), depth) {
		return nil
	}
	return inventory.publish(relative)
}

func nativePluginDirectory(name string, depth int) error {
	if depth > 4 || slices.Contains([]string{"node_modules", ".git", "sessions"}, name) {
		return filepath.SkipDir
	}
	return nil
}

func nativePluginManifest(path, name string, depth int) bool {
	if !slices.Contains([]string{"plugin.json", "mcp.json", nativeProjectMCPConfig, "marketplace.json"}, name) {
		return false
	}
	// Only package-root manifests and the native hidden manifest directory.
	return depth == 3 || depth == 4 && slices.Contains([]string{".codex-plugin", ".claude-plugin"}, filepath.Base(filepath.Dir(path)))
}

func (inventory *nativePluginInventory) publish(relative string) error {
	inventory.count++
	if inventory.count > 1024 {
		return errors.New("native plugin manifest inventory exceeds supported limit")
	}
	return inventory.add(filepath.Join(".codex/plugins/cache", relative), "CODEX_HOME", filepath.Join("plugins/cache", relative))
}
