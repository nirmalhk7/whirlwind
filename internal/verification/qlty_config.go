package verification

import (
	"path/filepath"
	"strings"
)

func ChangesQltyConfiguration(files []string) bool {
	for _, file := range files {
		clean := filepath.ToSlash(filepath.Clean(file))
		base := filepath.Base(clean)
		if clean == ".qlty" || strings.HasPrefix(clean, ".qlty/") || strings.Contains("/"+clean, "/.qlty/") || base == ".qltyignore" || base == ".qlty.toml" || base == "qlty.toml" || base == "qlty.yaml" || base == "qlty.yml" {
			return true
		}
	}
	return false
}
