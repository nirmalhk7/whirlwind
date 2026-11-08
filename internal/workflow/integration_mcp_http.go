package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
)

const integrationMCPResponseLimit = 2 << 20

type httpMCPUpstream struct {
	url      *url.URL
	headers  map[string]string
	client   *http.Client
	nextID   atomic.Int64
	mu       sync.Mutex
	ready    bool
	session  string
	protocol string
}

func (upstream *httpMCPUpstream) ListTools(ctx context.Context) ([]mcpRemoteTool, error) {
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if err := upstream.initialize(ctx); err != nil {
		return nil, err
	}
	var result struct {
		Tools []mcpRemoteTool `json:"tools"`
	}
	if err := upstream.request(ctx, "tools/list", map[string]any{}, &result); err != nil {
		return nil, err
	}
	return result.Tools, nil
}

func (upstream *httpMCPUpstream) initialize(ctx context.Context) error {
	if upstream.ready {
		return nil
	}
	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := upstream.request(ctx, "initialize", map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "whirlwind-read-broker", "version": "1"}}, &result); err != nil {
		return err
	}
	if result.ProtocolVersion != "2025-11-25" && result.ProtocolVersion != "2025-06-18" && result.ProtocolVersion != "2025-03-26" && result.ProtocolVersion != "2024-11-05" {
		return errors.New("selected HTTP MCP server negotiated an unsupported protocol version")
	}
	upstream.protocol = result.ProtocolVersion
	if err := upstream.notifyInitialized(ctx); err != nil {
		return err
	}
	upstream.ready = true
	return nil
}

func (upstream *httpMCPUpstream) notifyInitialized(ctx context.Context) (resultErr error) {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream.url.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	upstream.prepareRequest(request)
	response, err := upstream.client.Do(request)
	if err != nil {
		return errors.New("selected HTTP MCP initialization notification failed")
	}
	defer func() { resultErr = errors.Join(resultErr, response.Body.Close()) }()
	if response.StatusCode != http.StatusAccepted {
		return fmt.Errorf("selected HTTP MCP server rejected initialization notification with status %d", response.StatusCode)
	}
	_, resultErr = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
	return resultErr
}

func (upstream *httpMCPUpstream) CallTool(ctx context.Context, name string, arguments map[string]any) (json.RawMessage, error) {
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if err := upstream.initialize(ctx); err != nil {
		return nil, err
	}
	var result json.RawMessage
	if err := upstream.request(ctx, "tools/call", map[string]any{"name": name, "arguments": arguments}, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (upstream *httpMCPUpstream) request(ctx context.Context, method string, params any, result any) (requestErr error) {
	id := upstream.nextID.Add(1)
	body, err := encodeMCPRequest(id, method, params)
	if err != nil {
		return err
	}
	response, err := upstream.sendMCPRequest(ctx, body)
	if err != nil {
		return err
	}
	defer func() { requestErr = errors.Join(requestErr, response.Body.Close()) }()
	return upstream.decodeMCPResponse(response, id, result)
}

func encodeMCPRequest(id int64, method string, params any) ([]byte, error) {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	return body, nil
}

func (upstream *httpMCPUpstream) sendMCPRequest(ctx context.Context, body []byte) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream.url.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	upstream.prepareRequest(request)
	response, err := upstream.client.Do(request)
	if err != nil {
		return nil, errors.New("selected HTTP MCP connection request failed")
	}
	return response, nil
}

func (upstream *httpMCPUpstream) decodeMCPResponse(response *http.Response, id int64, result any) error {
	if err := upstream.validateMCPResponseHeaders(response); err != nil {
		return err
	}
	data, err := readMCPResponse(response.Body)
	if err != nil {
		return errors.New("selected HTTP MCP response exceeded supported size")
	}
	data, err = normalizeMCPResponse(data, response.Header.Get("Content-Type"), id)
	if err != nil {
		return err
	}
	envelope, err := decodeMCPEnvelope(data, id)
	if err != nil {
		return err
	}
	return decodeMCPResult(envelope.Result, result)
}

func decodeMCPEnvelope(data []byte, id int64) (mcpJSONRPCResult, error) {
	var envelope mcpJSONRPCResult
	if err := json.Unmarshal(data, &envelope); err != nil || !validMCPResponse(envelope, id) {
		return mcpJSONRPCResult{}, errors.New("selected HTTP MCP server returned an invalid JSON-RPC response")
	}
	return envelope, nil
}

func decodeMCPResult(raw json.RawMessage, result any) error {
	if err := json.Unmarshal(raw, result); err != nil {
		return errors.New("selected HTTP MCP server returned an invalid result")
	}
	return nil
}

func (upstream *httpMCPUpstream) validateMCPResponseHeaders(response *http.Response) error {
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("selected HTTP MCP server returned status %d", response.StatusCode)
	}
	if session := response.Header.Get("Mcp-Session-Id"); session != "" {
		upstream.session = session
	}
	return nil
}

type mcpJSONRPCResult struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

func normalizeMCPResponse(data []byte, contentType string, id int64) ([]byte, error) {
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if mediaType == "text/event-stream" {
		eventData, err := integrationMCPEventResponse(data, id)
		if err != nil {
			return nil, err
		}
		data = eventData
	} else if mediaType != "application/json" {
		return nil, errors.New("selected HTTP MCP server returned an unsupported content type")
	}
	return data, nil
}

func validMCPResponse(envelope mcpJSONRPCResult, id int64) bool {
	validID := string(envelope.ID) == fmt.Sprint(id)
	validResult := len(envelope.Result) > 0
	validError := len(envelope.Error) == 0 || string(envelope.Error) == "null"
	return validID && validResult && validError
}

func readMCPResponse(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, integrationMCPResponseLimit+1))
	if err != nil || len(data) > integrationMCPResponseLimit {
		return nil, errors.New("selected HTTP MCP response exceeded supported size")
	}
	return data, nil
}

func (upstream *httpMCPUpstream) prepareRequest(request *http.Request) {
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	if upstream.protocol != "" {
		request.Header.Set("MCP-Protocol-Version", upstream.protocol)
	}
	for name, value := range upstream.headers {
		request.Header.Set(name, value)
	}
	if upstream.session != "" {
		request.Header.Set("Mcp-Session-Id", upstream.session)
	}
}
