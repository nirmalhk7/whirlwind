package store

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

type runLogStorage struct {
	root  string
	mu    sync.Mutex
	feeds map[string]*persistedRunLog
}

type persistedRunLog struct {
	feed           runLogFeed
	size           int64
	detailsHash    string
	detailsChecked bool
	detailsDirty   bool
}

func newRunLogStorage(databasePath string) *runLogStorage {
	return &runLogStorage{root: filepath.Join(filepath.Dir(databasePath), "run-logs"), feeds: map[string]*persistedRunLog{}}
}

func (logs *runLogStorage) path(id string) (string, error) {
	if id == "" || id == "." || id == ".." || filepath.Base(id) != id {
		return "", errors.New("invalid run log ID")
	}
	return filepath.Join(logs.root, id+".log"), nil
}

// RunLogPath returns the already-written file, backfilling pre-file runs once.
func (s *Store) RunLogPath(ctx context.Context, id string) (string, error) {
	if _, err := s.Get(ctx, "sessions", id); err != nil {
		return "", err
	}
	if s.runLogs == nil {
		return "", errors.New("run log directory unavailable")
	}
	path, err := s.runLogs.path(id)
	if err != nil {
		return "", err
	}
	return path, s.runLogs.ensureFile(ctx, s, id, path)
}

func (s *Store) syncRunLogChange(ctx context.Context, kind, id string) error {
	if kind != "sessions" || s.runLogs == nil {
		return nil
	}
	if _, err := s.Get(ctx, kind, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	return s.runLogs.sync(ctx, s, id, nil)
}

func (s *Store) syncRunLogChanges(ctx context.Context, changes []Change) error {
	for _, change := range changes {
		if err := s.recordRunLogChange(ctx, change.Kind, change.ID); err != nil {
			return err
		}
	}
	return nil
}

func (logs *runLogStorage) sync(ctx context.Context, s *Store, id string, details []byte) (resultErr error) {
	logs.mu.Lock()
	defer logs.mu.Unlock()
	path, err := logs.path(id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(logs.root, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, lock.Close()) }()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, unix.Flock(int(lock.Fd()), unix.LOCK_UN)) }()
	return logs.appendFile(ctx, s, id, path, details)
}

func (logs *runLogStorage) appendFile(ctx context.Context, s *Store, id, path string, details []byte) (resultErr error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	state, writer, err := logs.resumeFile(file, s, id)
	if err != nil {
		return err
	}
	if err := state.append(ctx, writer, details); err != nil {
		delete(logs.feeds, id)
		return fmt.Errorf("persist run log: %w", err)
	}
	return logs.completeFile(file, state, id)
}

func (logs *runLogStorage) resumeFile(file *os.File, s *Store, id string) (*persistedRunLog, io.Writer, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	state := logs.feeds[id]
	if state != nil && state.size == info.Size() {
		_, err := file.Seek(0, io.SeekEnd)
		return state, file, err
	}
	state = &persistedRunLog{feed: runLogFeed{database: s, id: id, seen: map[string]bool{}}}
	return state, &runLogReplayWriter{file: file, reader: bufio.NewReader(file), state: state}, nil
}

func (logs *runLogStorage) completeFile(file *os.File, state *persistedRunLog, id string) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() != state.size {
		if err := file.Sync(); err != nil {
			return err
		}
	}
	state.size = info.Size()
	if state.detailsDirty {
		if err := os.WriteFile(file.Name()+".details", []byte(state.detailsHash), 0600); err != nil {
			return err
		}
		state.detailsDirty = false
	}
	logs.feeds[id] = state
	if state.feed.finished {
		delete(logs.feeds, id)
	}
	return nil
}

// Replay verifies existing bytes and appends only the missing suffix after restart.
type runLogReplayWriter struct {
	file   *os.File
	reader *bufio.Reader
	state  *persistedRunLog
	offset int64
}

func (writer *runLogReplayWriter) Write(body []byte) (int, error) {
	if writer.reader == nil {
		return writer.file.Write(body)
	}
	previous, err := writer.nextLine()
	n := len(previous)
	if n > len(body) || !bytes.Equal(previous, body[:n]) {
		return 0, errors.New("run log file does not match saved events")
	}
	if err == nil {
		return len(body), nil
	}
	if err != io.EOF && err != io.ErrUnexpectedEOF {
		return n, err
	}
	writer.reader = nil
	written, err := writer.file.Write(body[n:])
	return n + written, err
}
