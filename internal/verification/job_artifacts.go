package verification

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

type CommandArtifact struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Location string `json:"location"`
	SHA256   string `json:"sha256,omitempty"`
	Bytes    int64  `json:"bytes"`
}

const maximumArtifactFileBytes = 16 << 20
const maximumCommandArtifactBytes = 128 << 20
const maximumCommandArtifacts = 128

func (r *jobRunner) captureArtifacts(stage string, index int, outputs []string) ([]CommandArtifact, error) {
	source, err := os.OpenRoot(stage)
	if err != nil {
		return nil, err
	}
	defer func() { _ = source.Close() }()
	capture := artifactCapture{source: source, target: r.root, index: index, seen: map[string]bool{}}
	for _, output := range outputs {
		if err := fs.WalkDir(source.FS(), filepath.ToSlash(filepath.Clean(output)), capture.retain); err != nil {
			return capture.artifacts, fmt.Errorf("retain declared artifact %s: %w", output, err)
		}
	}
	return capture.artifacts, nil
}

type artifactCapture struct {
	source, target *os.Root
	index          int
	artifacts      []CommandArtifact
	seen           map[string]bool
	total          int64
}

func (c *artifactCapture) retain(path string, entry fs.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	if c.seen[path] {
		return nil
	}
	if len(c.artifacts) >= maximumCommandArtifacts {
		return errors.New("command artifact count exceeds retained limit")
	}
	info, err := entry.Info()
	if err != nil {
		return err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return errors.New("artifact links and special files are forbidden")
	}
	artifact := CommandArtifact{Path: path, Location: artifactLocation(c.index, path), Kind: "directory"}
	if info.IsDir() {
		if err := c.target.MkdirAll(artifact.Location, 0700); err != nil {
			return err
		}
	} else {
		if err := c.retainFile(&artifact); err != nil {
			return err
		}
	}
	c.seen[path] = true
	c.artifacts = append(c.artifacts, artifact)
	return nil
}

func (c *artifactCapture) retainFile(artifact *CommandArtifact) error {
	artifact.Kind = "file"
	file, err := c.source.Open(artifact.Path)
	if err != nil {
		return err
	}
	content, readErr := io.ReadAll(io.LimitReader(file, maximumArtifactFileBytes+1))
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	artifact.Bytes = int64(len(content))
	c.total += artifact.Bytes
	if artifact.Bytes > maximumArtifactFileBytes || c.total > maximumCommandArtifactBytes {
		return errors.New("command artifact bytes exceed retained limit")
	}
	hash := sha256.Sum256(content)
	artifact.SHA256 = hex.EncodeToString(hash[:])
	if err := c.target.MkdirAll(filepath.Dir(artifact.Location), 0700); err != nil {
		return err
	}
	retained, err := c.target.OpenFile(artifact.Location, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := retained.Write(content)
	if err := errors.Join(writeErr, retained.Sync(), retained.Close()); err != nil {
		return err
	}
	return nil
}

func artifactLocation(index int, path string) string {
	return filepath.ToSlash(filepath.Join("artifacts", fmt.Sprintf("%03d", index), path))
}

func validateRetainedArtifacts(m JobManifest, index int, result JobResult) error {
	command := m.Commands[index]
	if len(command.OutputPaths) == 0 && len(result.Artifacts) == 0 {
		return nil
	}
	if len(result.Artifacts) > maximumCommandArtifacts {
		return errors.New("artifact count exceeds retained limit")
	}
	var root *os.Root
	if len(result.Artifacts) > 0 {
		var err error
		root, err = os.OpenRoot(m.Root)
		if err != nil {
			return fmt.Errorf("retained artifact root unavailable: %w", err)
		}
		defer func() { _ = root.Close() }()
	}
	seen := map[string]bool{}
	var total int64
	for _, artifact := range result.Artifacts {
		if err := validateRetainedArtifact(root, index, command.OutputPaths, artifact, seen, &total); err != nil {
			return err
		}
	}
	if result.Status == "pass" {
		for _, output := range command.OutputPaths {
			if !seen[filepath.Clean(output)] {
				return errors.New("successful command omitted declared artifact evidence")
			}
		}
	}
	return nil
}

func declaredArtifactPath(path string, outputs []string) bool {
	for _, output := range outputs {
		rel, err := filepath.Rel(filepath.Clean(output), path)
		if err == nil && (rel == "." || filepath.IsLocal(rel)) {
			return true
		}
	}
	return false
}

func validateRetainedArtifact(root *os.Root, index int, outputs []string, artifact CommandArtifact, seen map[string]bool, total *int64) error {
	if !filepath.IsLocal(artifact.Path) || filepath.Clean(artifact.Path) != artifact.Path || artifact.Path == "." || seen[artifact.Path] || artifact.Location != artifactLocation(index, artifact.Path) {
		return errors.New("invalid retained artifact identity")
	}
	if !declaredArtifactPath(artifact.Path, outputs) {
		return errors.New("undeclared retained artifact")
	}
	seen[artifact.Path] = true
	info, err := root.Lstat(artifact.Location)
	if err != nil {
		return fmt.Errorf("retained artifact missing: %w", err)
	}
	if artifact.Kind == "directory" {
		if !info.IsDir() || artifact.Bytes != 0 || artifact.SHA256 != "" {
			return errors.New("invalid retained artifact directory")
		}
		return nil
	}
	return validateRetainedArtifactFile(root, info, artifact, total)
}

func validateRetainedArtifactFile(root *os.Root, info fs.FileInfo, artifact CommandArtifact, total *int64) error {
	if artifact.Kind != "file" || !info.Mode().IsRegular() || artifact.Bytes < 0 || artifact.Bytes > maximumArtifactFileBytes || info.Size() != artifact.Bytes {
		return errors.New("invalid retained artifact file")
	}
	*total += artifact.Bytes
	if *total > maximumCommandArtifactBytes {
		return errors.New("artifact bytes exceed retained limit")
	}
	file, err := root.Open(artifact.Location)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, readErr := io.Copy(hash, io.LimitReader(file, maximumArtifactFileBytes+1))
	if err := errors.Join(readErr, file.Close()); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != artifact.SHA256 {
		return errors.New("retained artifact hash mismatch")
	}
	return nil
}
