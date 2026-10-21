package memorybridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

func Search(ctx context.Context, address, token, query string) (string, error) {
	target, err := url.Parse(address)
	if err != nil || target.Hostname() != "127.0.0.1" || target.Scheme != "http" || token == "" {
		return "", errConnection
	}
	body, _ := json.Marshal(Query{Query: query})
	request, err := http.NewRequestWithContext(ctx, "POST", address, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	client := http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()
	return memoryResponse(response)
}

func memoryResponse(response *http.Response) (string, error) {
	body, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil {
		return "", err
	}
	if len(body) > 4096 {
		return "", fmt.Errorf("memory response exceeds limit")
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("memory search: %s", bytes.TrimSpace(body))
	}
	var value struct {
		Result string `json:"result"`
	}
	err = json.Unmarshal(body, &value)
	return value.Result, err
}
