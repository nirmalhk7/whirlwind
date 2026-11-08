package workflow

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/verification"
)

func runSnapshotCheck(ctx context.Context, scratch, snapshot string, check model.Check, current model.Project) (verification.Result, execution.Profile, error) {
	profile, err := reviewerSnapshotProfile(scratch, snapshot, current)
	if err != nil {
		return verification.Result{}, execution.Profile{}, err
	}
	effective, err := reviewerCheckExecution(profile, check)
	if err != nil {
		return verification.Result{}, execution.Profile{}, err
	}
	report, err := verification.RunObservedWithProfile(ctx, snapshot, []model.Check{check}, profile, nil)
	if err != nil {
		return verification.Result{}, execution.Profile{}, err
	}
	if len(report.Results) != 1 {
		return verification.Result{}, execution.Profile{}, errors.New("check runner returned no result")
	}
	return report.Results[0], effective, nil
}

func reviewerSnapshotProfile(scratch, snapshot string, current model.Project) (execution.Profile, error) {
	profile, err := execution.ForPolicy(snapshot, true, current.PolicyHash, current.Policy.Sandbox)
	if err != nil {
		return execution.Profile{}, err
	}
	profile.RuntimeDir = filepath.Join(scratch, "runtime")
	if err = os.Mkdir(profile.RuntimeDir, 0700); err != nil {
		return execution.Profile{}, err
	}
	return profile, nil
}

func reviewerCheckExecution(profile execution.Profile, check model.Check) (execution.Profile, error) {
	effective, err := execution.ForCheck(profile, check.OutputPaths)
	if err != nil {
		return execution.Profile{}, err
	}
	return execution.ForCommand(effective, check.Command)
}

// Reject links rather than allowing archive extraction to follow a path outside the snapshot.
func extractSnapshot(root string, input io.Reader) error {
	reader := tar.NewReader(io.LimitReader(input, 128*1024*1024+1))
	var total int64
	for count := 0; ; count++ {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err = validateSnapshotEntry(count, header, total); err != nil {
			return err
		}
		if header.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		if err = writeSnapshotEntry(root, reader, header); err != nil {
			return err
		}
		total += header.Size
	}
}

func validateSnapshotEntry(count int, header *tar.Header, total int64) error {
	if count >= 20000 || header.Size < 0 || header.Size > 16*1024*1024 || total+header.Size > 128*1024*1024 {
		return errors.New("review snapshot exceeds limits")
	}
	name := filepath.Clean(header.Name)
	if !filepath.IsLocal(name) || name == ".git" || strings.HasPrefix(name, ".git/") {
		return errors.New("unsafe review snapshot path")
	}
	return nil
}

func writeSnapshotEntry(root string, reader io.Reader, header *tar.Header) error {
	name := filepath.Clean(header.Name)
	target := filepath.Join(root, name)
	switch header.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(target, 0700)
	case tar.TypeReg:
		return writeSnapshotFile(target, reader, header)
	case tar.TypeXGlobalHeader:
		return nil
	default:
		return errors.New("review snapshot links and special files are unsupported")
	}
}

func writeSnapshotFile(target string, reader io.Reader, header *tar.Header) error {
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	mode := os.FileMode(0600)
	if header.Mode&0111 != 0 {
		mode = 0700
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(file, reader)
	closeErr := file.Close()
	return errors.Join(copyErr, closeErr)
}
