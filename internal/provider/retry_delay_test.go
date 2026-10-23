package provider

import (
	"encoding/json"
	"testing"
	"time"
)

// Ignoring the provider's structured delay would retry before the provider permits it.
func TestRetryAfterUsesStructuredProviderDelay(t *testing.T) {
	err := &RPCError{Code: 429, Message: "slow down", Data: json.RawMessage(`{"retryAfter":45}`)}
	if delay := RetryAfter(err); delay != 45*time.Second {
		t.Fatalf("provider delay ignored: %s", delay)
	}
}

func TestRetryAfterNeverShortensLongReportedDelay(t *testing.T) {
	err := &RPCError{Code: 429, Data: json.RawMessage(`{"retryAfter":259200}`)}
	if delay := RetryAfter(err); delay != 72*time.Hour {
		t.Fatalf("reported cooldown shortened: %s", delay)
	}
}
