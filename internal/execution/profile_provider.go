package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"whirlwind/internal/model"
)

func ForProvider(cwd string, readOnly bool, hash string, policy *model.SandboxPolicy, command []string) (Profile, error) {
	p, err := ForPolicy(cwd, readOnly, hash, policy)
	if err != nil {
		return p, err
	}
	b, _ := json.Marshal(command)
	sum := sha256.Sum256(b)
	p.CommandHash = hex.EncodeToString(sum[:])
	if openShellLauncher(p.Launcher) {
		return p, nil
	}
	// Missing programs still get a profile identity; the launch boundary refuses them.
	if len(command) == 0 {
		return p, nil
	}
	executable, err := exec.LookPath(command[0])
	if err != nil {
		return p, nil
	}
	roots, err := providerReadRoots(command, executable)
	if err != nil {
		return p, err
	}
	p.ReadRoots = append(p.ReadRoots, roots...)
	slices.Sort(p.ReadRoots)
	return ForCommand(p, command)
}

func providerReadRoots(command []string, executable string) ([]string, error) {
	paths := []string{executable}
	for _, arg := range command[1:] {
		if !filepath.IsAbs(arg) {
			continue
		}
		if info, err := os.Stat(arg); err == nil && info.Mode().IsRegular() {
			paths = append(paths, arg)
		}
	}
	var roots []string
	for _, path := range paths {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, err
		}
		roots = append(roots, resolved)
		if packageRoot, ok := providerPackageRoot(resolved); ok {
			roots = append(roots, packageRoot)
		}
	}
	return roots, nil
}

func providerPackageRoot(path string) (string, bool) {
	switch filepath.Ext(path) {
	case ".js", ".mjs", ".cjs":
		dir := filepath.Dir(path)
		for depth := 0; depth < 4; depth++ {
			if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
				return nodeModuleRoot(dir), true
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "", false
}

// Node resolves transitive dependencies through the containing installation's
// node_modules tree. Grant installed code, never the installation's parent.
func nodeModuleRoot(packageRoot string) string {
	for directory := packageRoot; directory != filepath.Dir(directory); directory = filepath.Dir(directory) {
		if filepath.Base(directory) == "node_modules" {
			return directory
		}
	}
	return packageRoot
}
