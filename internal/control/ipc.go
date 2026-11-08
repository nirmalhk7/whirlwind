package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	maxIPCMessage = 1 << 20
	maxDiffCursor = 64 << 20
	ipcTimeout    = 15 * time.Second
)

// Request is the bounded local supervisor protocol. Operation is a named
// command, never an HTTP path or arbitrary method dispatch.
type Request struct {
	RequestID       string          `json:"request_id"`
	Operation       string          `json:"operation"`
	ProjectID       string          `json:"project_id,omitempty"`
	PlanID          string          `json:"plan_id,omitempty"`
	ResourceID      string          `json:"resource_id,omitempty"`
	ExpectedVersion int             `json:"expected_version,omitempty"`
	ContentHash     string          `json:"content_hash,omitempty"`
	IdempotencyKey  string          `json:"idempotency_key,omitempty"`
	EventCursor     int64           `json:"event_cursor,omitempty"`
	DiffOffset      int64           `json:"diff_offset,omitempty"`
	Limit           int             `json:"limit,omitempty"`
	Payload         json.RawMessage `json:"payload,omitempty"`
}

type Response struct {
	RequestID string `json:"request_id"`
	Result    any    `json:"result,omitempty"`
	Error     string `json:"error,omitempty"`
}

type Dispatcher interface {
	Handle(context.Context, Request) (Response, error)
}

type LocalServer struct {
	listener net.Listener
	path     string
	info     os.FileInfo
	mu       sync.Mutex
	conns    map[net.Conn]struct{}
	closed   bool
}

type Client struct{ path string }

var allowedOperations = map[string]bool{
	"inspect.snapshot": true, "inspect.records": true, "inspect.events": true, "inspect.plan": true, "inspect.artifact": true, "supervisor.stop": true,
	"project.register": true, "ideation.start": true,
	"provider.status":  true,
	"direction.select": true, "direction.approve": true,
	"direction.reject": true, "plan.review": true,
	"direction.reconsider": true,
	"question.answer":      true, "permission.decide": true,
	"plan.pause": true, "plan.resume": true,
	"finalization.inspect": true, "finalization.approve": true,
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

func validateRequest(request Request) error {
	if strings.TrimSpace(request.RequestID) == "" || len(request.RequestID) > 128 {
		return errors.New("request_id is required and must be at most 128 characters")
	}
	if !allowedOperations[request.Operation] {
		return fmt.Errorf("unsupported local operation %q", request.Operation)
	}
	if len(request.Payload) > maxIPCMessage/2 {
		return errors.New("command payload exceeds 512 KiB")
	}
	if request.Operation == "inspect.events" {
		if request.EventCursor < 0 || request.Limit < 0 || request.Limit > 500 {
			return errors.New("event cursor or page limit is invalid (limit 1..500)")
		}
		return nil
	}
	if request.Operation == "finalization.inspect" && (request.DiffOffset < 0 || request.DiffOffset > maxDiffCursor) {
		return errors.New("finalization diff cursor is outside the supported range")
	}
	if strings.HasPrefix(request.Operation, "inspect.") || request.Operation == "finalization.inspect" || request.Operation == "provider.status" {
		return nil
	}
	if request.IdempotencyKey == "" || len(request.IdempotencyKey) > 256 {
		return errors.New("idempotency_key is required for supervisor commands")
	}
	if request.Operation != "project.register" && request.Operation != "supervisor.stop" && request.ExpectedVersion < 1 {
		return errors.New("expected_version must be positive for this supervisor command")
	}
	if request.Operation == "plan.review" && request.ContentHash == "" {
		return errors.New("content_hash is required for plan review")
	}
	if len(request.Payload) > 0 && !json.Valid(request.Payload) {
		return errors.New("payload must be valid JSON")
	}
	return nil
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

func NewClient(path string) *Client { return &Client{path: path} }

// SocketPath selects the private local supervisor endpoint inside the state directory.
func SocketPath(dataDirectory string) string {
	absolute, err := filepath.Abs(dataDirectory)
	if err != nil {
		absolute = filepath.Clean(dataDirectory)
	}
	sum := sha256.Sum256([]byte(absolute))
	return filepath.Join("/tmp", fmt.Sprintf("whirlwind-%d", os.Getuid()), hex.EncodeToString(sum[:8])+".sock")
}

func (c *Client) Call(ctx context.Context, request Request) (Response, error) {
	if err := validateRequest(request); err != nil {
		return Response{}, err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", c.path)
	if err != nil {
		return Response{}, fmt.Errorf("connect to local supervisor at %s: %w", c.path, err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(ipcTimeout))
	}
	if err = json.NewEncoder(conn).Encode(request); err != nil {
		return Response{}, err
	}
	if unixConn, ok := conn.(*net.UnixConn); ok {
		if err = unixConn.CloseWrite(); err != nil {
			return Response{}, err
		}
	}
	var response Response
	if err = json.NewDecoder(io.LimitReader(conn, maxIPCMessage)).Decode(&response); err != nil {
		return Response{}, err
	}
	if response.RequestID != request.RequestID {
		return Response{}, errors.New("local supervisor response request_id mismatch")
	}
	if response.Error != "" {
		return Response{}, errors.New(response.Error)
	}
	return response, nil
}
