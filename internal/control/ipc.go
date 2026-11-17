package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type LocalServer struct {
	listener net.Listener
	path     string
	info     os.FileInfo
	mu       sync.Mutex
	conns    map[net.Conn]struct{}
	closed   bool
}

// Listen creates a private local control socket. It refuses to repair a
// directory whose permissions are already broader than the current user.
func Listen(path string, dispatcher Dispatcher) (*LocalServer, error) {
	if dispatcher == nil {
		return nil, errors.New("local supervisor dispatcher is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(absolute), 0700); err != nil {
		return nil, fmt.Errorf("create private control directory: %w", err)
	}
	dir, err := os.Lstat(filepath.Dir(absolute))
	if err != nil {
		return nil, err
	}
	if dir.Mode()&os.ModeSymlink != 0 || !dir.IsDir() || dir.Mode().Perm()&0077 != 0 || !ownedByCurrentUser(dir) {
		return nil, fmt.Errorf("control directory %s must be an owned private directory (mode 0700)", filepath.Dir(absolute))
	}
	if err = removeStaleSocket(absolute); err != nil {
		return nil, err
	}
	listener, err := net.Listen("unix", absolute)
	if err != nil {
		return nil, fmt.Errorf("listen on local control socket: %w", err)
	}
	if err = os.Chmod(absolute, 0600); err != nil {
		_ = listener.Close()
		_ = os.Remove(absolute)
		return nil, fmt.Errorf("secure local control socket: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		_ = listener.Close()
		_ = os.Remove(absolute)
		return nil, err
	}
	server := &LocalServer{listener: listener, path: absolute, info: info, conns: make(map[net.Conn]struct{})}
	go server.serve(dispatcher)
	return server, nil
}

func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Getuid()
}

func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to replace non-socket control path %s", path)
	}
	conn, dialErr := net.DialTimeout("unix", path, 250*time.Millisecond)
	if dialErr == nil {
		_ = conn.Close()
		return fmt.Errorf("local supervisor already owns control socket %s", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale control socket: %w", err)
	}
	return nil
}

func (s *LocalServer) serve(dispatcher Dispatcher) {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = conn.Close()
			return
		}
		s.conns[conn] = struct{}{}
		s.mu.Unlock()
		go s.handle(conn, dispatcher)
	}
}

func (s *LocalServer) handle(conn net.Conn, dispatcher Dispatcher) {
	defer func() {
		_ = conn.Close()
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
	}()
	_ = conn.SetDeadline(time.Now().Add(ipcTimeout))
	limited := io.LimitReader(conn, maxIPCMessage+1)
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		s.writeError(conn, request.RequestID, "invalid JSON request")
		return
	}
	if err := validateRequest(request); err != nil {
		s.writeError(conn, request.RequestID, err.Error())
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		s.writeError(conn, request.RequestID, "request must contain one JSON object")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), ipcTimeout)
	defer cancel()
	response, err := dispatcher.Handle(ctx, request)
	if err != nil {
		s.writeError(conn, request.RequestID, err.Error())
		return
	}
	response.RequestID = request.RequestID
	encoded, err := json.Marshal(response)
	if err != nil {
		s.writeError(conn, request.RequestID, "local supervisor response is not valid JSON")
		return
	}
	if len(encoded)+1 > maxIPCMessage {
		s.writeError(conn, request.RequestID, "local supervisor response exceeds 1 MiB; request a smaller inspection page")
		return
	}
	_, _ = conn.Write(append(encoded, '\n'))
}

func (s *LocalServer) writeError(conn net.Conn, requestID, message string) {
	_ = json.NewEncoder(conn).Encode(Response{RequestID: requestID, Error: message})
}

// Close stops accepts and in-flight local requests, then removes only the
// socket inode created by this server.
func (s *LocalServer) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	for conn := range s.conns {
		_ = conn.Close()
	}
	s.mu.Unlock()
	err := s.listener.Close()
	if current, statErr := os.Lstat(s.path); statErr == nil && os.SameFile(current, s.info) {
		if removeErr := os.Remove(s.path); err == nil {
			err = removeErr
		}
	}
	return err
}
