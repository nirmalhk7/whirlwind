package verification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	autofixProposalPattern = "proposals/%03d.patch"
)

type AutofixProposal struct {
	PatchPath       string `json:"patch_path"`
	SHA256          string `json:"sha256"`
	Bytes           int64  `json:"bytes"`
	CandidateCommit string `json:"candidate_commit"`
	CandidateTree   string `json:"candidate_tree"`
}

func (r *jobRunner) prepareFixCheckout(ctx context.Context, index int) (string, error) {
	relative := autofixCheckoutDir(index)
	if err := r.root.MkdirAll(filepath.Dir(relative), 0700); err != nil {
		return "", err
	}
	stage, err := r.cloneDisposableCheckout(ctx, relative)
	if err != nil {
		return "", err
	}
	if _, err := r.git(ctx, stage, "checkout", "--detach", r.manifest.CandidateCommit); err != nil {
		return "", err
	}
	if err := r.prepareQualityConfiguration(stage); err != nil {
		return "", err
	}
	if err := r.validateFrozenCheckout(ctx, stage); err != nil {
		return "", err
	}
	// Keep candidate bytes, but expose approved base as HEAD for commands such
	// as qlty check --upstream HEAD. Added candidate files remain untracked.
	if _, err := r.git(ctx, stage, "reset", "--mixed", r.manifest.BaseCommit); err != nil {
		return "", err
	}
	return stage, nil
}

func (r *jobRunner) captureAutofix(ctx context.Context, stage string, index int, command JobCommand) (*AutofixProposal, error) {
	head, err := r.git(ctx, stage, "rev-parse", "HEAD")
	if err != nil || head != r.manifest.BaseCommit {
		return nil, errors.New("autofix changed protected Git identity")
	}
	if err := r.validateQualityConfiguration(stage); err != nil {
		return nil, err
	}
	paths := []string{".", ":(exclude,literal).qlty"}
	for _, path := range append(r.qualityPaths(), command.OutputPaths...) {
		paths = append(paths, ":(exclude,literal)"+path)
	}
	// Intent-to-add includes newly created source in the binary patch, while
	// excluding scanner-owned configuration, caches and declared build outputs.
	if _, err := r.git(ctx, stage, append([]string{"add", "--intent-to-add", "--all", "--"}, paths...)...); err != nil {
		return nil, err
	}
	patch, err := r.gitRaw(ctx, stage, append([]string{"diff", "--binary", r.manifest.CandidateCommit, "--"}, paths...)...)
	if err != nil {
		return nil, err
	}
	if patch == "" {
		return nil, nil
	}
	if len(patch) > maximumArtifactFileBytes {
		return nil, errors.New("autofix patch exceeds bounded artifact size")
	}
	if err := r.root.MkdirAll("proposals", 0700); err != nil {
		return nil, err
	}
	path := fmt.Sprintf(autofixProposalPattern, index)
	file, err := r.root.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	_, writeErr := file.WriteString(patch)
	if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		return nil, err
	}
	hash := sha256.Sum256([]byte(patch))
	return &AutofixProposal{PatchPath: filepath.Join(r.root.Name(), path), SHA256: hex.EncodeToString(hash[:]), Bytes: int64(len(patch)), CandidateCommit: r.manifest.CandidateCommit, CandidateTree: r.manifest.CandidateTree}, nil
}

func validateAutofixProposal(m JobManifest, index int, result JobResult) error {
	proposal := result.Autofix
	if proposal == nil {
		return nil
	}
	canonicalRoot, err := filepath.EvalSymlinks(m.Root)
	if err != nil {
		return err
	}
	want := filepath.Join(canonicalRoot, "proposals", fmt.Sprintf("%03d.patch", index))
	if m.Commands[index].Mode != "autofix" || proposal.PatchPath != want || proposal.CandidateCommit != m.CandidateCommit || proposal.CandidateTree != m.CandidateTree || proposal.Bytes <= 0 || proposal.Bytes > maximumArtifactFileBytes {
		return errors.New("autofix proposal scope or identity mismatch")
	}
	root, err := os.OpenRoot(m.Root)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	path := fmt.Sprintf(autofixProposalPattern, index)
	info, err := root.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != proposal.Bytes {
		return errors.New("autofix proposal missing or unsafe")
	}
	file, err := root.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(file, maximumArtifactFileBytes+1))
	if err != nil || n != proposal.Bytes || hex.EncodeToString(hash.Sum(nil)) != proposal.SHA256 {
		return errors.New("autofix proposal digest mismatch")
	}
	return nil
}

// ReadAutofixPatch rechecks exact bytes before the supervisor retains a patch;
// source-writing children never receive write access to this artifact location.
func ReadAutofixPatch(m JobManifest, index int, result JobResult) ([]byte, error) {
	if index < 0 || index >= len(m.Commands) || result.Autofix == nil {
		return nil, errors.New("autofix proposal required")
	}
	if err := validateAutofixProposal(m, index, result); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(m.Root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open(fmt.Sprintf(autofixProposalPattern, index))
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, maximumArtifactFileBytes+1))
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(content)
	if int64(len(content)) != result.Autofix.Bytes || hex.EncodeToString(hash[:]) != result.Autofix.SHA256 {
		return nil, errors.New("autofix proposal changed while being retained")
	}
	return content, nil
}
