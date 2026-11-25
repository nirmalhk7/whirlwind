package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"whirlwind/internal/process"
)

// freezeHookSnapshot preserves HEAD and the candidate's tracked/nonignored
// files in a private checkout. Git commands inside checks can inspect the diff.
func freezeHookSnapshot(ctx context.Context, source, destination string) (snapshotID string, resultErr error) {
	head, err := hookGit(ctx, source, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", err
	}
	revision := strings.TrimSpace(head)
	if _, err = hookGit(ctx, source, "clone", "--no-checkout", "--no-hardlinks", "--quiet", "--", source, destination); err != nil {
		return "", err
	}
	if _, err = hookGit(ctx, destination, "update-ref", "--no-deref", "HEAD", revision); err != nil {
		return "", err
	}
	if _, err = hookGit(ctx, destination, "read-tree", revision); err != nil {
		return "", err
	}
	target, err := os.OpenRoot(destination)
	if err != nil {
		return "", err
	}
	defer func() { resultErr = errors.Join(resultErr, target.Close()) }()
	identity, err := hookSnapshotIdentity(ctx, source, target)
	if err != nil {
		return "", err
	}
	frozen, err := hookSnapshotIdentity(ctx, destination, nil)
	if err != nil || frozen != identity {
		return "", errors.Join(errors.New("frozen hook snapshot does not match the worktree"), err)
	}
	return identity, nil
}

func hookSnapshotIdentity(ctx context.Context, source string, target *os.Root) (snapshotID string, resultErr error) {
	head, err := hookGit(ctx, source, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", err
	}
	files, err := hookGit(ctx, source, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return "", err
	}
	names := strings.Split(files, "\x00")
	slices.Sort(names)
	root, err := os.OpenRoot(source)
	if err != nil {
		return "", err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	digest := sha256.New()
	encoder := json.NewEncoder(digest)
	_ = encoder.Encode(strings.TrimSpace(head))
	for _, name := range slices.Compact(names) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if name == "" {
			continue
		}
		if !filepath.IsLocal(name) || name == "." || name == ".git" || strings.HasPrefix(name, ".git/") {
			return "", errors.New("invalid path in hook snapshot")
		}
		hash, err := hookSnapshotEntry(root, target, name)
		if err != nil {
			return "", fmt.Errorf("hook snapshot %q: %w", name, err)
		}
		if hash != "" {
			_ = encoder.Encode([2]string{name, hash})
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func hookSnapshotEntry(source, target *os.Root, name string) (fingerprint string, resultErr error) {
	info, err := source.Lstat(name)
	if os.IsNotExist(err) || errors.Is(err, syscall.ENOTDIR) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", nil
	}
	if target != nil {
		if err := target.MkdirAll(filepath.Dir(name), 0700); err != nil {
			return "", err
		}
	}
	if info.Mode()&os.ModeSymlink != 0 {
		link, err := source.Readlink(name)
		if err == nil && target != nil {
			err = target.Symlink(link, name)
		}
		return "link:" + link, err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("regular file or symlink required")
	}
	input, err := source.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", err
	}
	defer func() { resultErr = errors.Join(resultErr, input.Close()) }()
	actual, err := input.Stat()
	if err != nil || !os.SameFile(info, actual) || !actual.Mode().IsRegular() {
		return "", errors.Join(errors.New("file changed while freezing hook snapshot"), err)
	}
	hash := sha256.New()
	if target == nil {
		_, err = io.Copy(hash, input)
	} else {
		output, openErr := target.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if openErr != nil {
			return "", openErr
		}
		_, err = io.Copy(io.MultiWriter(output, hash), input)
		err = errors.Join(err, output.Chmod(info.Mode().Perm()), output.Close())
	}
	return fmt.Sprintf("%o:%x", info.Mode().Perm(), hash.Sum(nil)), err
}

func hookGit(ctx context.Context, cwd string, args ...string) (string, error) {
	git, err := commandTarget("git")
	if err != nil {
		return "", err
	}
	cmd := process.Command(ctx, git, append([]string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false"}, args...)...)
	cmd.Dir = cwd
	env := slices.DeleteFunc(cmd.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "GIT_") })
	cmd.Env = append(env, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_OPTIONAL_LOCKS=0")
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := process.Run(cmd); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, stderr.String())
	}
	return stdout.String(), nil
}
