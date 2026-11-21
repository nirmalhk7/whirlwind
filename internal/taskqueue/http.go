package taskqueue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"
)

const routePrefix = "/api/v1/queues/"

// Handler is for an OS-protected Unix socket only, never a TCP or webhook mux.
func Handler(backend Backend) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Host != "localhost" || r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" {
			http.Error(w, "private local queue endpoint", http.StatusForbidden)
			return
		}
		role, action, ok := parseRoute(r.URL.Path)
		if !ok || r.Method != http.MethodPost || r.URL.RawQuery != "" {
			http.NotFound(w, r)
			return
		}
		contentType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if contentType != "application/json" {
			http.Error(w, "application/json required", http.StatusUnsupportedMediaType)
			return
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		dec.DisallowUnknownFields()
		var result *Delivery
		var err error
		switch action {
		case "push":
			var request *Push
			err = decodeObject(dec, &request)
			if err == nil {
				err = backend.PushRoleTasks(r.Context(), role, *request)
			}
		case "pop":
			var request *struct{}
			err = decodeObject(dec, &request)
			if err == nil {
				result, err = backend.PopRoleTask(r.Context(), role)
			}
		default:
			var request *Receipt
			err = decodeObject(dec, &request)
			if err == nil {
				err = backend.SettleRoleTask(r.Context(), role, *request, action)
			}
		}
		switch {
		case errors.Is(err, ErrConflict):
			http.Error(w, "queue reference, phase, or lease conflict", http.StatusConflict)
		case errors.Is(err, errInput):
			http.Error(w, "valid JSON object with allowed fields required", http.StatusBadRequest)
		case err != nil:
			http.Error(w, "queue backend unavailable", http.StatusServiceUnavailable)
		case result == nil:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(result)
		}
	})
}

var errInput = errors.New("invalid queue input")

func decodeObject[T any](dec *json.Decoder, value **T) error {
	var extra any
	if dec.Decode(value) != nil || *value == nil || !errors.Is(dec.Decode(&extra), io.EOF) {
		return errInput
	}
	return nil
}

func parseRoute(path string) (Role, string, bool) {
	path, ok := strings.CutPrefix(path, routePrefix)
	if !ok {
		return "", "", false
	}
	role, action, ok := strings.Cut(path, "/")
	switch action {
	case "push", "pop", "ack", "release", "renew":
		return Role(role), action, ok && Role(role).Valid()
	default:
		return "", "", false
	}
}

type Client struct{ http *http.Client }

func NewClient(socket string) *Client {
	dialer := &net.Dialer{}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, "unix", socket)
	}}
	return NewClientWithTransport(transport)
}

// NewClientWithTransport runs the same HTTP protocol over a supplied transport.
// The production supervisor exclusively uses NewClient with its private socket.
func NewClientWithTransport(transport http.RoundTripper) *Client {
	return &Client{http: &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}}
}

func (c *Client) Close() { c.http.CloseIdleConnections() }

func (c *Client) Push(ctx context.Context, role Role, p Push) error {
	_, err := c.call(ctx, role, "push", p)
	return err
}

func (c *Client) Pop(ctx context.Context, role Role) (*Delivery, error) {
	return c.call(ctx, role, "pop", struct{}{})
}

func (c *Client) Settle(ctx context.Context, role Role, receipt Receipt, action string) error {
	_, err := c.call(ctx, role, action, receipt)
	return err
}

func (c *Client) call(ctx context.Context, role Role, action string, payload any) (*Delivery, error) {
	if !role.Valid() {
		return nil, ErrConflict
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost"+routePrefix+string(role)+"/"+action, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(r)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	switch res.StatusCode {
	case http.StatusNoContent:
		return nil, nil
	case http.StatusConflict:
		return nil, ErrConflict
	case http.StatusOK:
		var d Delivery
		if err := json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&d); err != nil {
			return nil, err
		}
		return &d, nil
	default:
		return nil, fmt.Errorf("queue %s %s returned HTTP %d", role, action, res.StatusCode)
	}
}
