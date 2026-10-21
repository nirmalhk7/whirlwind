package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func learningFingerprint(cwd, path string) (string, error) {
	if !validLearningPath(path) {
		return "", errors.New("invalid source path")
	}
	root, err := os.OpenRoot(cwd)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	return fingerprintLearningFile(file)
}

func validLearningPath(path string) bool {
	if path == "" || filepath.IsAbs(path) || filepath.ToSlash(filepath.Clean(path)) != path {
		return false
	}
	if path == ".." || strings.HasPrefix(path, "../") {
		return false
	}
	return path != ".git" && !strings.HasPrefix(path, ".git/")
}

func fingerprintLearningFile(file *os.File) (string, error) {
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > 1024*1024 {
		return "", errors.New("source is not bounded regular text")
	}
	sum := sha256.New()
	if _, err := io.Copy(sum, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}
