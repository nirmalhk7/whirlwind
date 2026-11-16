package httpapi

import "testing"

func requireAPISuccess(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
