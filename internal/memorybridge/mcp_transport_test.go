package memorybridge

import (
	"context"
	"errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemoryMCPListsAndCallsScopedToolThroughSDKTransport(t *testing.T) {
	var calls atomic.Int32
	session := connectMemoryMCP(t, func(_ context.Context, query string) (string, error) {
		calls.Add(1)
		if query != "contention" {
			return "", errors.New("unexpected query")
		}
		return "Use bounded retries", nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Name != "search_lessons" {
		t.Fatalf("registered tools: %+v", tools.Tools)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "search_lessons", Arguments: map[string]any{"query": "contention"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("tool result: %+v", result)
	}
	content, ok := result.Content[0].(*mcp.TextContent)
	if !ok || content.Text != "Use bounded retries" || calls.Load() != 1 {
		t.Fatalf("tool content=%+v calls=%d", content, calls.Load())
	}
}

func TestMemoryMCPPropagatesSearchFailureWithoutLessonContent(t *testing.T) {
	session := connectMemoryMCP(t, func(context.Context, string) (string, error) {
		return "", errors.New("memory search allowance exhausted")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "search_lessons", Arguments: map[string]any{"query": "contention"}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("search failure presented as successful advice: %+v", result)
	}
}

func connectMemoryMCP(t *testing.T, search Searcher) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := Server(search).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "memory-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestMemoryEndpointCancellationRejectsPostLeaseDelivery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	endpoint, err := Start(ctx, func(context.Context, string) (string, error) { calls.Add(1); return "lesson", nil })
	if err != nil {
		t.Fatal(err)
	}
	defer endpoint.Close()
	if _, err = Search(context.Background(), endpoint.URL, endpoint.Token, "before cancellation"); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-endpoint.done:
	case <-time.After(5 * time.Second):
		t.Fatal("memory credential lease remained active")
	}
	if _, err = Search(context.Background(), endpoint.URL, endpoint.Token, "after cancellation"); err == nil {
		t.Fatal("expired attempt accepted a memory call")
	}
	if calls.Load() != 1 {
		t.Fatalf("search deliveries=%d; want only pre-cancellation call", calls.Load())
	}
}
