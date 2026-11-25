package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"
)

type Client struct{ path string }

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
	defer func() {
		// The server may already have committed a decision. Report a socket
		// cleanup error without invalidating its received receipt.
		if err := conn.Close(); err != nil {
			log.Printf("close supervisor connection: %v", err)
		}
	}()
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
