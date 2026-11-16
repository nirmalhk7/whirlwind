package workflow

import "testing"

func requireWorkflowSuccess(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
