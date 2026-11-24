package verification

import (
	"os"
	"path/filepath"
	"strings"
)

// Qlty acquires and writes its installer lock even on an already-installed
// tool. Allow that on the candidate's private copy of an existing, empty,
// nonexecutable lock beside a completed package, not on package assets, the
// directory, or the retained approved tree. The identity still hashes all
// lock bytes, so incomplete cleanup or permanent edits fail closed.
func isQualifiedQltyInstallLock(root *os.Root, relative string, info os.FileInfo) bool {
	if !info.Mode().IsRegular() || info.Size() != 0 || info.Mode().Perm()&0111 != 0 {
		return false
	}
	parts := strings.Split(filepath.ToSlash(relative), "/")
	if len(parts) != 2 || !qualityToolDirectory.MatchString(parts[0]) {
		return false
	}
	base, hasLockSuffix := strings.CutSuffix(parts[1], ".lock")
	if !hasLockSuffix || !qualityPackageDirectory.MatchString(base) {
		return false
	}
	packagePath := filepath.Join(parts[0], base)
	pkg, err := root.Lstat(packagePath)
	if err != nil || !pkg.IsDir() {
		return false
	}
	done, err := root.Lstat(packagePath + ".done")
	return err == nil && done.Mode().IsRegular()
}
