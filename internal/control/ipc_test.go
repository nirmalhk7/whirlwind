package control

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type testDispatcher struct{}

func (testDispatcher) Handle(_ context.Context, request Request) (Response, error) {
	return Response{RequestID: request.RequestID, Result: request.Operation}, nil
}

func TestLocalSocketIsPrivateAndCorrelatesNamedCommands(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "ww-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	path := filepath.Join(base, "control", "control.sock")
	server, err := Listen(path, testDispatcher{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("socket mode = %o, want 600", info.Mode().Perm())
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	response, err := NewClient(path).Call(ctx, Request{RequestID: "request-1", Operation: "inspect.snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	if response.RequestID != "request-1" || response.Result != "inspect.snapshot" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestLocalSocketRejectsUnknownOperationsBeforeDispatch(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "ww-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	path := filepath.Join(base, "control", "control.sock")
	server, err := Listen(path, testDispatcher{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := NewClient(path).Call(ctx, Request{RequestID: "request-2", Operation: "raw.http"}); err == nil {
		t.Fatal("unknown raw operation was accepted")
	}
}

func TestLocalSocketAllowsVersionedDirectionReconsideration(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "ww-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	path := filepath.Join(base, "control", "control.sock")
	server, err := Listen(path, testDispatcher{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	request := Request{RequestID: "reconsider-request", Operation: "direction.reconsider", ProjectID: "project-a", ResourceID: "direction-a", ExpectedVersion: 7, IdempotencyKey: "reconsider-once", Payload: []byte(`{"project_id":"project-a","reason":"The project goal changed."}`)}
	response, err := NewClient(path).Call(ctx, request)
	if err != nil || response.RequestID != request.RequestID || response.Result != request.Operation {
		t.Fatalf("named human reconsideration command was not dispatched: %+v %v", response, err)
	}
}

func TestFinalizationDiffCursorIsBounded(t *testing.T) {
	for _, offset := range []int64{-1, maxDiffCursor + 1} {
		if err := validateRequest(Request{RequestID: "diff", Operation: "finalization.inspect", DiffOffset: offset}); err == nil {
			t.Errorf("finalization diff offset %d was accepted", offset)
		}
	}
}
