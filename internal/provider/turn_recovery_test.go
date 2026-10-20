package provider

import (
	"context"
	"testing"
)

func TestCodexTurnFailurePreservesHTTPStatus(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   FailureKind
	}{{429, FailureRateLimit}, {503, FailureTransient}, {401, FailureAuth}} {
		s := providerSession{ctx: context.Background(), rpc: &RPC{done: make(chan struct{})}, completed: make(chan string, 1)}
		s.turnCompleted(map[string]any{"turn": map[string]any{"status": "failed", "error": map[string]any{"message": "upstream rejected request", "codexErrorInfo": map[string]any{"httpConnectionFailed": map[string]any{"httpStatusCode": tc.status}}}}})
		err := s.waitCodexTurn()
		if ClassifyFailure(err) != tc.want {
			t.Errorf("status=%d kind=%s error=%v", tc.status, ClassifyFailure(err), err)
		}
	}
}
