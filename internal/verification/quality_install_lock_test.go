//go:build darwin || linux

package verification

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"whirlwind/internal/model"
)

func TestCandidateQualityCopyAllowsQualifiedInstallerLock(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "approved", "tools")
	dest := filepath.Join(root, "candidate", "tools")
	t.Cleanup(func() {
		_ = filepath.WalkDir(dest, func(path string, entry os.DirEntry, err error) error {
			if err == nil && entry.IsDir() {
				_ = os.Chmod(path, 0700)
			}
			return nil
		})
	})
	qualified := filepath.Join("gitleaks", "8.29.1-abcdef012345")
	nonempty := filepath.Join("gitleaks", "8.29.2-9876543210ab")
	unqualified := filepath.Join("gitleaks", "8.30.0-abcdef012345")
	for _, id := range []string{qualified, nonempty, unqualified} {
		requireVerificationSuccess(t, os.MkdirAll(filepath.Join(source, id), 0700))
		requireVerificationSuccess(t, os.WriteFile(filepath.Join(source, id, "gitleaks"), []byte("frozen scanner asset"), 0700))
	}
	for _, id := range []string{qualified, nonempty} {
		requireVerificationSuccess(t, os.WriteFile(filepath.Join(source, id+".done"), nil, 0400))
	}
	for path, contents := range map[string]string{
		qualified + ".lock":                         "",
		nonempty + ".lock":                          "nonempty cache record",
		unqualified + ".lock":                       "",
		filepath.Join(qualified, "package.lock"):    "",
		filepath.Join("gitleaks", "arbitrary.lock"): "",
	} {
		requireVerificationSuccess(t, os.WriteFile(filepath.Join(source, path), []byte(contents), 0400))
	}
	digest, err := hashQualityTree(source)
	requireVerificationSuccess(t, err)
	requireVerificationSuccess(t, copyApprovedQualityTree(context.Background(), source, dest, digest))
	exerciseCandidateInstallerLock(t, dest, qualified+".lock", digest)

	for _, path := range []string{qualified, filepath.Join(qualified, "gitleaks"), qualified + ".done",
		nonempty + ".lock", unqualified + ".lock", filepath.Join(qualified, "package.lock"), filepath.Join("gitleaks", "arbitrary.lock")} {
		info, err := os.Lstat(filepath.Join(dest, path))
		requireVerificationSuccess(t, err)
		if info.Mode().Perm()&0222 != 0 {
			t.Fatalf("copied scanner bundle unnecessarily permits writing %s: %s", path, info.Mode())
		}
	}
	info, err := os.Lstat(filepath.Join(source, qualified+".lock"))
	requireVerificationSuccess(t, err)
	if info.Mode().Perm() != 0400 || info.Size() != 0 {
		t.Fatalf("copy changed retained installation lock: %s size=%d", info.Mode(), info.Size())
	}
	for _, path := range []string{source, dest} {
		got, err := hashQualityTree(path)
		requireVerificationSuccess(t, err)
		if got != digest {
			t.Fatalf("normal lock/unlock changed frozen runtime identity for %s", path)
		}
	}
}

// Qlty v0.668.0 writes its PID before checking for an installed tool.
// fslock clears the lock bytes on normal unlock.
func exerciseCandidateInstallerLock(t *testing.T, dest, lockPath, digest string) {
	t.Helper()
	lock, err := os.OpenFile(filepath.Join(dest, lockPath), os.O_RDWR, 0)
	requireVerificationSuccess(t, err)
	t.Cleanup(func() { _ = lock.Close() })
	requireVerificationSuccess(t, unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB))
	_, err = fmt.Fprintln(lock, os.Getpid())
	requireVerificationSuccess(t, err)
	inFlight, err := hashQualityTree(dest)
	requireVerificationSuccess(t, err)
	if inFlight == digest {
		t.Fatal("lock bytes disappeared from runtime identity")
	}
	requireVerificationSuccess(t, unix.Flock(int(lock.Fd()), unix.LOCK_UN))
	requireVerificationSuccess(t, lock.Truncate(0))
}

func TestCaptureQualityIdentityNamesMissingCandidateEvidence(t *testing.T) {
	const runtimeDir = "/runtime"
	snapshot := &model.QualitySnapshot{}
	for _, tc := range []struct {
		name, output, runtime, message string
		frozen                         *model.QualitySnapshot
	}{
		{"no configuration", "{}", runtimeDir, "frozen quality configuration is missing", nil},
		{"no runtime", "{}", "", "isolated Qlty runtime directory is missing", snapshot},
		{"no stdout", "", runtimeDir, "Qlty command produced no SARIF on stdout", snapshot},
		{"oversized stdout", strings.Repeat("x", (1<<20)+1), runtimeDir, "Qlty SARIF exceeds the runtime identity evidence limit", snapshot},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := captureQualityIdentity(tc.output, tc.runtime, "qlty", tc.frozen, ""); got != nil || err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("expected precise fail-closed reason %q: identity=%+v err=%v", tc.message, got, err)
			}
		})
	}
}
