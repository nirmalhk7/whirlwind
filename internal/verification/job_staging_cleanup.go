package verification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	verificationCheckoutDir = "checkout"
	checkoutDraftPrefix     = "checkout-preparing-"
)

type checkoutOwnership struct {
	Manifest string
	Path     string
	Device   uint64
	Inode    uint64
	Draft    string `json:",omitempty"`
}

func autofixCheckoutDir(index int) string {
	return filepath.Join("fixes", fmt.Sprintf("%03d", index), verificationCheckoutDir)
}

func checkoutOwnershipFile(relative string) string {
	return "stage-" + strings.ReplaceAll(filepath.ToSlash(relative), "/", "-") + ".json"
}

// Record ownership before Git writes anything, then publish the complete
// checkout by renaming the same directory. A retry can discard a verified
// unpublished copy without touching an existing checkout.
func (r *jobRunner) cloneDisposableCheckout(ctx context.Context, relative string, prepare func(string) error) (string, error) {
	if _, err := r.root.Lstat(relative); err == nil {
		return "", fmt.Errorf("disposable verification checkout already exists: %s", relative)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := r.discardUnpublishedCheckout(relative); err != nil {
		return "", err
	}
	draft, err := os.MkdirTemp(r.root.Name(), checkoutDraftPrefix)
	if err != nil {
		return "", err
	}
	draftName := filepath.Base(draft)
	ownership, err := r.checkoutOwnership(draftName)
	if err != nil {
		return "", errors.Join(err, r.root.Remove(draftName))
	}
	ownership.Path, ownership.Draft = relative, draftName
	if err := writeJobJSON(r.root, checkoutOwnershipFile(relative), ownership); err != nil {
		return "", fmt.Errorf("record verification staging ownership: %w", errors.Join(err, r.root.Remove(draftName)))
	}
	if _, err := r.git(ctx, r.root.Name(), "clone", "--no-hardlinks", "--no-checkout", "--", r.manifest.Repository, draft); err != nil {
		return "", err
	}
	if err := prepare(draft); err != nil {
		return "", err
	}
	if err := r.root.Rename(draftName, relative); err != nil {
		return "", err
	}
	return filepath.Join(r.root.Name(), relative), nil
}

func (r *jobRunner) discardUnpublishedCheckout(relative string) error {
	saved, err := r.readCheckoutOwnership(relative)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if saved.Draft != "" {
		if err := r.removeOwnedCheckout(saved.Draft, saved); err != nil {
			return err
		}
	}
	return r.root.Remove(checkoutOwnershipFile(relative))
}

// Only a published, validated report reaches this point, while the runner
// still holds the job lock. Uncertain executions can need their partial stage.
func (r *jobRunner) releaseCompletedStaging(report JobReport) error {
	for i, result := range report.Results {
		if (r.started[i] || result.Runtime.ExecutionID != "") && !result.Runtime.CleanupVerified {
			return nil
		}
	}
	err := r.releaseCheckout(verificationCheckoutDir, report)
	for index, command := range r.manifest.Commands {
		if command.Mode == "autofix" {
			err = errors.Join(err, r.releaseCheckout(autofixCheckoutDir(index), report))
		}
	}
	return err
}

func (r *jobRunner) releaseCheckout(relative string, report JobReport) error {
	stage := filepath.Join(r.root.Name(), relative)
	if report.QualityIdentity != nil && qualityPathsOverlap(stage, report.QualityIdentity.ToolRuntimePath) ||
		r.manifest.Quality != nil && r.manifest.Quality.Identity != nil && qualityPathsOverlap(stage, r.manifest.Quality.Identity.ToolRuntimePath) {
		return nil // Keep any legacy location still bound to qualified tools.
	}
	current, err := r.checkoutOwnership(relative)
	if errors.Is(err, os.ErrNotExist) {
		return r.discardUnpublishedCheckout(relative)
	}
	if err != nil {
		return err
	}
	saved, err := r.readCheckoutOwnership(relative)
	if errors.Is(err, os.ErrNotExist) {
		return nil // Legacy/unmarked: never delete it.
	}
	if err != nil {
		return err
	}
	if err := saved.verifyDirectory(current); err != nil {
		return err
	}
	if err := r.root.RemoveAll(relative); err != nil {
		return err
	}
	return r.root.Remove(checkoutOwnershipFile(relative))
}

func (r *jobRunner) removeOwnedCheckout(relative string, saved checkoutOwnership) error {
	current, err := r.checkoutOwnership(relative)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := saved.verifyDirectory(current); err != nil {
		return err
	}
	return r.root.RemoveAll(relative)
}

func (r *jobRunner) readCheckoutOwnership(relative string) (checkoutOwnership, error) {
	marker := checkoutOwnershipFile(relative)
	info, err := r.root.Lstat(marker)
	if err != nil {
		return checkoutOwnership{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > 512 {
		return checkoutOwnership{}, fmt.Errorf("invalid verification staging marker: %s", marker)
	}
	raw, err := r.root.ReadFile(marker)
	if err != nil {
		return checkoutOwnership{}, err
	}
	var saved checkoutOwnership
	if json.Unmarshal(raw, &saved) != nil || saved.Manifest != r.manifest.Digest() || saved.Path != relative || saved.Inode == 0 ||
		saved.Draft != "" && (filepath.Base(saved.Draft) != saved.Draft || !strings.HasPrefix(saved.Draft, checkoutDraftPrefix)) {
		return checkoutOwnership{}, fmt.Errorf("unrecognized verification staging owner: %s", relative)
	}
	return saved, nil
}

func (saved checkoutOwnership) verifyDirectory(current checkoutOwnership) error {
	if saved.Device != current.Device || saved.Inode != current.Inode {
		return fmt.Errorf("unrecognized verification staging owner: %s", current.Path)
	}
	return nil
}

func (r *jobRunner) checkoutOwnership(relative string) (checkoutOwnership, error) {
	parts := strings.Split(relative, string(filepath.Separator))
	var info os.FileInfo
	for i := range parts {
		path := filepath.Join(parts[:i+1]...)
		var err error
		info, err = r.root.Lstat(path)
		if err != nil {
			return checkoutOwnership{}, err
		}
		if !info.IsDir() {
			return checkoutOwnership{}, fmt.Errorf("verification staging path is not a plain directory: %s", path)
		}
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Ino == 0 {
		return checkoutOwnership{}, errors.New("verification staging directory lacks a stable filesystem identity")
	}
	return checkoutOwnership{Manifest: r.manifest.Digest(), Path: relative, Device: uint64(stat.Dev), Inode: uint64(stat.Ino)}, nil
}
