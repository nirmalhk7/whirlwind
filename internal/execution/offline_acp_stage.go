package execution

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type offlineACPStage struct {
	source, target, owningDir string
	base                      []byte
	mode                      os.FileMode
}

func offlineACPChild(parent, path string) bool {
	relative, err := filepath.Rel(parent, path)
	return err == nil && filepath.IsLocal(relative) && relative != "."
}

func (p Profile) offlineACPWriteStage(registry string) (*offlineACPStage, error) {
	data := filepath.Dir(registry)
	if p.ReadOnly {
		if len(p.WriteRoots) == 0 {
			return nil, nil
		}
		if p.offlineACPRole != "planner_output" || len(p.WriteRoots) != 1 || !offlineACPChild(filepath.Join(data, "planning"), p.CWD) {
			return nil, errors.New("offline ACP writer requires a designated Planner output")
		}
		path := p.WriteRoots[0]
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 ||
			filepath.Dir(path) != p.CWD || !strings.HasPrefix(filepath.Base(path), ".whirlwind-plan-output-") {
			return nil, errors.New("offline ACP Planner output is outside its assigned scope")
		}
		return &offlineACPStage{source: path, target: path}, nil
	}
	// The offline documentation fixture supports a single pre-existing, exact
	// Markdown path authorized by the unit and project. No directories, add,
	// delete, pattern, executable source, or Git metadata grants.
	if p.offlineACPRole != "coder_documentation" || !offlineACPChild(filepath.Join(data, "worktrees"), p.CWD) || len(p.AllowedPaths) != 1 || len(p.WriteRoots) != 0 {
		return nil, errors.New("offline ACP Coder requires one approved documentation path")
	}
	rel := p.AllowedPaths[0]
	if !filepath.IsLocal(rel) || filepath.Clean(rel) != rel || filepath.Base(rel) != rel || filepath.Ext(rel) != ".md" || strings.HasPrefix(rel, ".") {
		return nil, errors.New("offline ACP Coder accepts one exact Markdown filename")
	}
	target := filepath.Join(p.CWD, rel)
	raw, mode, err := loadOfflineACPFile(target)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(data, "offline-acp-stage-")
	if err != nil {
		return nil, err
	}
	file := filepath.Join(dir, rel)
	if err := os.WriteFile(file, raw, 0600); err != nil {
		return nil, errors.Join(err, os.RemoveAll(dir))
	}
	return &offlineACPStage{source: file, target: target, owningDir: dir, base: raw, mode: mode}, nil
}

func loadOfflineACPFile(path string) ([]byte, os.FileMode, error) {
	const max = 1 << 20
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 != 0 || info.Size() > max {
		return nil, 0, errors.New("offline ACP staging requires a small regular nonexecutable file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil || !os.SameFile(fi, info) {
		return nil, 0, errors.New("offline ACP staged file changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil || len(raw) > max {
		return nil, 0, errors.Join(errors.New("offline ACP file size exceeds limit"), err)
	}
	return raw, info.Mode().Perm(), nil
}

func (conn *OfflineACPConnection) FinishWrites(commit bool) (failure error) {
	s := conn.stage
	if s == nil || s.owningDir == "" {
		return nil
	}
	// Keep staging intact when cleanup is unverified. Removing a live bind's
	// host source would discard evidence and would not remove its writable mount.
	if !conn.cleanupVerified {
		return nil
	}
	defer func() { failure = errors.Join(failure, os.RemoveAll(s.owningDir)) }()
	if !commit {
		return nil
	}
	old, mode, err := loadOfflineACPFile(s.target)
	if err != nil || mode != s.mode || !bytes.Equal(old, s.base) {
		return errors.Join(errors.New("offline ACP candidate changed before staged import"), err)
	}
	next, _, err := loadOfflineACPFile(s.source)
	if err != nil {
		return err
	}
	if bytes.Equal(old, next) {
		return nil
	}
	// Serialize with the approved worktree lease and write only this exact path.
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	temp := filepath.Join(filepath.Dir(s.target), ".ww-stage-"+hex.EncodeToString(nonce[:]))
	f, err := os.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, s.mode)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temp) }()
	_, err = f.Write(next)
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	if err := os.Rename(temp, s.target); err != nil {
		return fmt.Errorf("import approved staged documentation: %w", err)
	}
	return nil
}
