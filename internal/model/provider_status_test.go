package model

import (
	"encoding/json"
	"testing"
)

func TestProviderStatusWireContract(t *testing.T) {
	status := ProviderStatus{ID: "codex", Name: "Codex", Kind: "acp", Status: "paused", Detail: "stopped"}
	encoded, err := json.Marshal(ProviderStatuses{Providers: []ProviderStatus{status}, IntervalSeconds: 30, Stopped: true})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"providers":[{"quota_fresh":null,"id":"codex","name":"Codex","kind":"acp","status":"paused","detail":"stopped"}],"interval_seconds":30,"stopped":true}`
	if string(encoded) != want {
		t.Fatalf("status wire contract changed: %s", encoded)
	}
}
