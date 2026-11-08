package provider

import "testing"

func TestACPRPCStatusPreservesRetryCategories(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   FailureKind
	}{{429, FailureRateLimit}, {503, FailureTransient}, {401, FailureAuth}} {
		err := &RPCError{Code: tc.status, Message: "ACP request failed"}
		if got := ClassifyFailure(err); got != tc.want {
			t.Errorf("status=%d kind=%s want=%s", tc.status, got, tc.want)
		}
	}
}
