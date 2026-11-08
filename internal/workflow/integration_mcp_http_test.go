package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const (
	mcpTestJSONContentType   = "application/json"
	mcpTestContentTypeHeader = "Content-Type"
	mcpTestInitialized       = "notifications/initialized"
)

func TestHTTPMCPUpstreamNegotiatesAndInitializesBeforeSelectedReads(t *testing.T) {
	methods := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { serveMCPNegotiation(t, &methods, w, r) }))
	defer server.Close()
	upstream := &httpMCPUpstream{client: server.Client(), headers: map[string]string{}, protocol: "", url: mustURL(t, server.URL)}
	tools, err := upstream.ListTools(context.Background())
	if err != nil || len(tools) != 1 || tools[0].Name != "read_project" {
		t.Fatalf("selected HTTP MCP catalog failed: tools=%+v err=%v", tools, err)
	}
	if _, err := upstream.CallTool(context.Background(), "read_project", map[string]any{"repo": "o/r"}); err != nil {
		t.Fatalf("selected HTTP MCP call failed: %v", err)
	}
	want := []string{"initialize", mcpTestInitialized, "tools/list", "tools/call"}
	if fmt.Sprint(methods) != fmt.Sprint(want) {
		t.Fatalf("MCP initialization order changed: got %v want %v", methods, want)
	}
}

func serveMCPNegotiation(t *testing.T, methods *[]string, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	var request struct {
		ID     int    `json:"id"`
		Method string `json:"method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		t.Errorf("decode request: %v", err)
		return
	}
	*methods = append(*methods, request.Method)
	if !strings.Contains(r.Header.Get("Accept"), mcpTestJSONContentType) || !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		t.Errorf("MCP POST omitted required response types: %q", r.Header.Get("Accept"))
	}
	if request.Method != "initialize" && (r.Header.Get("MCP-Protocol-Version") != "2025-11-25" || r.Header.Get("Mcp-Session-Id") != "session-1") {
		t.Errorf("post-initialize request omitted negotiated headers: %#v", r.Header)
	}
	if request.Method == mcpTestInitialized {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set(mcpTestContentTypeHeader, mcpTestJSONContentType)
	if request.Method == "initialize" {
		w.Header().Set("Mcp-Session-Id", "session-1")
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-11-25"}}`, request.ID)
		return
	}
	_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[{"name":"read_project","inputSchema":{"type":"object"},"annotations":{"readOnlyHint":true}}]}}`, request.ID)
}

func TestHTTPMCPUpstreamReadsMatchingSSEResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		if request.Method == "initialize" {
			w.Header().Set(mcpTestContentTypeHeader, mcpTestJSONContentType)
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-11-25"}}`, request.ID)
			return
		}
		if request.Method == mcpTestInitialized {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"tools\":[]}}\n\n", request.ID)
	}))
	defer server.Close()
	upstream := &httpMCPUpstream{client: server.Client(), headers: map[string]string{}, url: mustURL(t, server.URL)}
	tools, err := upstream.ListTools(context.Background())
	if err != nil || len(tools) != 0 {
		t.Fatalf("selected HTTP MCP SSE catalog failed: tools=%+v err=%v", tools, err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

type failingCloseBody struct{ io.Reader }

func (failingCloseBody) Close() error { return errors.New("body close failed") }

func TestHTTPMCPInitializationReportsResponseCloseFailure(t *testing.T) {
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusAccepted, Header: make(http.Header), Body: failingCloseBody{Reader: strings.NewReader("")}}, nil
	})}
	upstream := &httpMCPUpstream{client: client, url: mustURL(t, "http://mcp.test"), headers: map[string]string{}}
	if err := upstream.notifyInitialized(context.Background()); err == nil || !strings.Contains(err.Error(), "body close failed") {
		t.Fatalf("response close failure was lost: %v", err)
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
