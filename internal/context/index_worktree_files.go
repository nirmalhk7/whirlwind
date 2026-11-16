package context

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const sourceFileLimit = 256 * 1024

func workingTreeRoot(cwd string) (string, error) {
	root, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", err
	}
	return filepath.Abs(root)
}

func safeSourcePath(path string) bool {
	clean, safe := cleanSourcePath(path)
	if !safe {
		return false
	}
	if !safeSourceComponents(clean) {
		return false
	}
	return safeSourceName(clean)
}

func cleanSourcePath(path string) (string, bool) {
	if path == "" || filepath.IsAbs(path) {
		return "", false
	}
	clean := filepath.Clean(path)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", false
	}
	return clean, true
}

func safeSourceComponents(clean string) bool {
	for _, part := range strings.Split(filepath.ToSlash(clean), "/") {
		if part == ".git" || part == "node_modules" {
			return false
		}
	}
	return true
}

func safeSourceName(clean string) bool {
	base := strings.ToLower(filepath.Base(clean))
	if environmentSourceName(base) || namedCredentialSource(base) {
		return false
	}
	for _, suffix := range []string{".pem", ".key", ".p12", ".pfx", ".jks", ".keystore"} {
		if strings.HasSuffix(base, suffix) {
			return false
		}
	}
	return true
}

func environmentSourceName(base string) bool {
	return base == ".env" || strings.HasPrefix(base, ".env.")
}

func namedCredentialSource(base string) bool {
	switch base {
	case "credentials", "credentials.json", "secrets", "secrets.json":
		return true
	default:
		return false
	}
}

func readWorkingTreeEntry(ctx context.Context, root, path string) (entry Entry, present, excluded bool, resultErr error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, false, false, err
	}
	symlink, err := hasSymlinkComponent(root, path)
	if err != nil {
		return Entry{}, false, false, err
	}
	if symlink {
		return Entry{}, false, true, nil
	}
	rootFS, err := os.OpenRoot(root)
	if err != nil {
		return Entry{}, false, false, err
	}
	defer func() { resultErr = errors.Join(resultErr, rootFS.Close()) }()
	return readWorkingTreeEntryFromRoot(ctx, root, path, rootFS)
}

func readWorkingTreeEntryFromRoot(ctx context.Context, root, path string, rootFS *os.Root) (entry Entry, present, excluded bool, resultErr error) {
	file, err := rootFS.Open(filepath.FromSlash(path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Entry{}, false, false, nil
		}
		return Entry{}, false, false, err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	return readSourceFile(ctx, root, path, file)
}

func readSourceFile(ctx context.Context, root, path string, file *os.File) (Entry, bool, bool, error) {
	excluded, err := excludedSourceFile(ctx, root, path, file)
	if err != nil {
		return Entry{}, false, false, err
	}
	if excluded {
		return Entry{}, false, true, nil
	}
	contents, err := io.ReadAll(io.LimitReader(file, sourceFileLimit+1))
	if err != nil {
		return Entry{}, false, false, err
	}
	return sourceEntryFromBytes(path, contents)
}

func excludedSourceFile(ctx context.Context, root, path string, file *os.File) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	symlink, err := hasSymlinkComponent(root, path)
	if err != nil {
		return false, err
	}
	if symlink {
		return true, nil
	}
	return excludedFileMetadata(file)
}

func excludedFileMetadata(file *os.File) (bool, error) {
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return true, nil
	}
	return info.Size() > sourceFileLimit, nil
}

func sourceEntryFromBytes(path string, contents []byte) (Entry, bool, bool, error) {
	if oversizedSource(contents) {
		return Entry{}, false, true, nil
	}
	if sourceContainsNUL(contents) {
		return Entry{}, false, true, nil
	}
	return sourceEntry(path, contents), true, false, nil
}

func oversizedSource(contents []byte) bool {
	return len(contents) > sourceFileLimit
}

func sourceContainsNUL(contents []byte) bool {
	return bytes.IndexByte(contents, 0) >= 0
}

func hasSymlinkComponent(root, path string) (bool, error) {
	current := root
	parts := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	for _, part := range parts {
		current = filepath.Join(current, filepath.FromSlash(part))
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return true, nil
		}
	}
	return false, nil
}
