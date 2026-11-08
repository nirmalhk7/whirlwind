package control

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type unavailableInspection struct{}

func (unavailableInspection) Handle(context.Context, Request) (Response, error) {
	return Response{}, errors.New("agent inspection unavailable")
}

func TestSupervisorErrorRetainsRequestCorrelation(t *testing.T) {
	path := SocketPath(t.TempDir())
	server, err := Listen(path, unavailableInspection{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	_, err = NewClient(path).Call(context.Background(), Request{RequestID: "agents-error", Operation: "inspect.records", ResourceID: "agents"})
	if err == nil || !strings.Contains(err.Error(), "agent inspection unavailable") {
		t.Fatalf("supervisor error hidden behind protocol mismatch: %v", err)
	}
}
