package model

import "testing"

func TestIssueExecutionRequiresReadyLabel(t *testing.T) {
	for _, tc := range []struct {
		name, state string
		labels      []string
		want        bool
	}{
		{"ready", "OPEN", []string{"whirlwind:ready"}, true},
		{"closed", "CLOSED", []string{"whirlwind:ready"}, false},
		{"plan is not ready", "OPEN", []string{"whirlwind:plan"}, false},
		{"unlabeled", "open", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := (Issue{State: tc.state, Labels: tc.labels}).Eligible(); got != tc.want {
				t.Fatalf("eligible=%v want=%v", got, tc.want)
			}
		})
	}
}
