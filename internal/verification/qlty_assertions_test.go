package verification

import "testing"

func requireVerificationSuccess(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
