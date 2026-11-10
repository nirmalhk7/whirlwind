package control

import "testing"

func TestRunnerToggleCommandRequiresRetryIdentityWithoutSnapshotVersion(t *testing.T) {
	request := Request{RequestID: "toggle", Operation: "runners.toggle", IdempotencyKey: "toggle-once"}
	if err := validateRequest(request); err != nil {
		t.Fatalf("toggle must operate on current supervisor state: %v", err)
	}
	request.IdempotencyKey = ""
	if err := validateRequest(request); err == nil {
		t.Fatal("toggle accepted without retry identity")
	}
}
