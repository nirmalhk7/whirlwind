package verification

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"whirlwind/internal/process"
)

const (
	jobJournalFilename = "events.jsonl"
)

type jobRunner struct {
	manifest JobManifest
	root     *os.Root
	lock     *os.File
	journal  *os.File
	results  map[int]JobResult
	started  map[int]bool
}

func openJob(m JobManifest) (*jobRunner, error) {
	rootPath, err := verificationRootPath(m)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	runner := &jobRunner{manifest: m, root: root, results: map[int]JobResult{}, started: map[int]bool{}}
	runner.lock, err = root.OpenFile("job.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err == nil {
		err = syscall.Flock(int(runner.lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	}
	if err != nil {
		runner.close()
		return nil, fmt.Errorf("verification job is already running or unavailable: %w", err)
	}
	err = validateJobManifestFile(root, m)
	if err != nil {
		runner.close()
		return nil, err
	}
	if err := runner.loadJournal(); err != nil {
		runner.close()
		return nil, err
	}
	runner.journal, err = root.OpenFile(jobJournalFilename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		runner.close()
		return nil, err
	}
	return runner, nil
}

func (r *jobRunner) close() {
	if r.journal != nil {
		_ = r.journal.Close()
	}
	if r.lock != nil {
		_ = r.lock.Close()
	}
	if r.root != nil {
		_ = r.root.Close()
	}
}

func (r *jobRunner) loadJournal() error {
	raw, err := r.root.ReadFile(jobJournalFilename)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(raw) > 16<<20 {
		return errors.New("verification journal exceeds bounded size")
	}
	raw, err = r.truncateIncompleteJournal(raw)
	if err != nil {
		return err
	}
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		if err := r.restoreJournalEvent(line); err != nil {
			return err
		}
	}
	return nil
}

func (r *jobRunner) record(event JobEvent, observe func(JobEvent) error) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if len(raw) > 64<<10 {
		return errors.New("verification progress event exceeds 64 KiB")
	}
	if _, err := r.journal.Write(append(raw, '\n')); err != nil {
		return err
	}
	if err := r.journal.Sync(); err != nil {
		return err
	}
	if observe != nil {
		return observe(event)
	}
	return nil
}

func (r *jobRunner) git(ctx context.Context, cwd string, args ...string) (string, error) {
	raw, err := r.gitRaw(ctx, cwd, args...)
	return strings.TrimSpace(raw), err
}

func (r *jobRunner) gitRaw(ctx context.Context, cwd string, args ...string) (string, error) {
	command := process.Command(ctx, "git", append([]string{"-c", "core.hooksPath=/dev/null", "-C", cwd}, args...)...)
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Join(r.root.Name(), "host-home"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0"}
	for _, entry := range command.Env {
		if strings.HasPrefix(entry, "WHIRLWIND_PROCESS_TOKEN=") {
			env = append(env, entry)
		}
	}
	command.Env = env
	var output capped
	command.Stdout, command.Stderr = &output, &output
	err := process.Run(command)
	if err != nil {
		return "", fmt.Errorf("staging Git failed: %w: %s", err, string(output.data))
	}
	if output.total > output.Len() {
		return "", errors.New("staging Git output exceeds bounded size")
	}
	return string(output.data), nil
}

func (r *jobRunner) prepareCheckout(ctx context.Context) (string, error) {
	stage := filepath.Join(r.root.Name(), verificationCheckoutDir)
	if err := r.root.MkdirAll("host-home", 0700); err != nil {
		return "", err
	}
	current, err := r.checkoutOwnership(verificationCheckoutDir)
	if errors.Is(err, os.ErrNotExist) {
		return r.cloneDisposableCheckout(ctx, verificationCheckoutDir, func(draft string) error {
			if err := r.populateDisposableCheckout(ctx, draft); err != nil {
				return err
			}
			return r.validateCheckout(ctx, draft)
		})
	}
	if err != nil {
		return "", err
	}
	saved, err := r.readCheckoutOwnership(verificationCheckoutDir)
	if err == nil {
		if err := saved.verifyDirectory(current); err != nil {
			return "", err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := r.validateCheckout(ctx, stage); err != nil {
		return "", err
	}
	return stage, nil
}

func (r *jobRunner) populateDisposableCheckout(ctx context.Context, stage string) error {
	if _, err := r.git(ctx, stage, "checkout", "--detach", r.manifest.CandidateCommit); err != nil {
		return err
	}
	return r.prepareQualityConfiguration(stage)
}

func (r *jobRunner) validateCheckout(ctx context.Context, stage string) error {
	head, err := r.git(ctx, stage, "rev-parse", "HEAD")
	if err != nil || head != r.manifest.CandidateCommit {
		return errors.New("verification checkout candidate mismatch")
	}
	tree, err := r.git(ctx, stage, "rev-parse", "HEAD^{tree}")
	if err != nil || tree != r.manifest.CandidateTree {
		return errors.New("verification checkout tree mismatch")
	}
	if _, err := r.git(ctx, stage, "cat-file", "-e", r.manifest.BaseCommit+"^{commit}"); err != nil {
		return err
	}
	return r.validateFrozenCheckout(ctx, stage)
}

func verificationRootPath(m JobManifest) (string, error) {
	repository, err := filepath.EvalSymlinks(m.Repository)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(m.Root, 0700); err != nil {
		return "", err
	}
	info, err := os.Lstat(m.Root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return "", errors.New("verification root must be a private directory, not a link")
	}
	rootPath, err := filepath.EvalSymlinks(m.Root)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(repository, rootPath)
	if err != nil {
		return "", err
	}
	reverse, err := filepath.Rel(rootPath, repository)
	if err != nil {
		return "", err
	}
	if filepath.IsLocal(relative) || filepath.IsLocal(reverse) {
		return "", errors.New("verification staging must be outside the original repository")
	}
	return rootPath, nil
}

func validateJobManifestFile(root *os.Root, m JobManifest) error {
	raw, err := root.ReadFile("manifest.json")
	if os.IsNotExist(err) {
		err = writeJobJSON(root, "manifest.json", m)
	} else if err == nil {
		var saved JobManifest
		err = json.Unmarshal(raw, &saved)
		if err == nil && saved.Digest() != m.Digest() {
			err = errors.New("verification journal belongs to another manifest/candidate/configuration")
		}
	}
	return err
}

func (r *jobRunner) truncateIncompleteJournal(raw []byte) ([]byte, error) {
	complete := bytes.LastIndexByte(raw, '\n') + 1
	if complete != len(raw) {
		file, err := r.root.OpenFile(jobJournalFilename, os.O_WRONLY, 0600)
		if err != nil {
			return nil, err
		}
		err = file.Truncate(int64(complete))
		err = errors.Join(err, file.Close())
		if err != nil {
			return nil, err
		}
		raw = raw[:complete]
	}
	return raw, nil
}

func (r *jobRunner) restoreJournalEvent(line []byte) error {
	var event JobEvent
	if err := json.Unmarshal(line, &event); err != nil {
		return err
	}
	if event.ManifestHash != r.manifest.Digest() || event.Index < 0 || event.Index >= len(r.manifest.Commands) {
		return errors.New("verification journal identity mismatch")
	}
	switch event.Type {
	case "started":
		if r.started[event.Index] || r.results[event.Index].Name != "" {
			return errors.New("duplicate verification start in journal")
		}
		r.started[event.Index] = true
	case "result":
		if event.Result == nil || r.results[event.Index].Name != "" || event.Result.Name != r.manifest.Commands[event.Index].Name || event.Result.ManifestHash != r.manifest.Digest() {
			return errors.New("invalid verification result in journal")
		}
		r.results[event.Index] = *event.Result
	default:
		return errors.New("unknown verification journal event")
	}
	return nil
}
