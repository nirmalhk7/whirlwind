package httpapi

import (
	"os"
	"path/filepath"
	"strings"
)

type directoryEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type directoryListing struct {
	Path    string           `json:"path"`
	Parent  string           `json:"parent"`
	Entries []directoryEntry `json:"entries"`
}

func (q apiRequest) directories() error {
	listing, err := browseDirectory(q.r.URL.Query().Get("path"))
	if err != nil {
		return err
	}
	reply(q.w, 200, listing)
	return nil
}

func browseDirectory(path string) (directoryListing, error) {
	listing := directoryListing{Entries: []directoryEntry{}}
	canonical, err := directoryPath(path)
	if err != nil {
		return listing, err
	}
	entries, err := os.ReadDir(canonical)
	if err != nil {
		return listing, err
	}
	listing.Path = canonical
	listing.Parent = filepath.Dir(canonical)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := filepath.Join(canonical, entry.Name())
		if browsableDirectory(entry, path) {
			listing.Entries = append(listing.Entries, directoryEntry{Name: entry.Name(), Path: path})
		}
	}
	return listing, nil
}

func directoryPath(path string) (string, error) {
	if path == "" {
		var err error
		path, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	if !filepath.IsAbs(path) {
		return "", &apiError{422, "invalid_input", "Absolute directory path required"}
	}
	return filepath.EvalSymlinks(path)
}

func browsableDirectory(entry os.DirEntry, path string) bool {
	if entry.IsDir() {
		return true
	}
	if entry.Type()&os.ModeSymlink == 0 {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
