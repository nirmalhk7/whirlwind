package context

import (
	"encoding/json"
	"testing"
)

func TestActionMarkersIncludeStableSourceLocations(t *testing.T) {
	entry := sourceEntry("service.go", []byte("package service\n// TODO: validate cache\nfunc run(){}\n// FIXME stale responses\n"))
	raw, _ := json.Marshal(entry)
	var value map[string]any
	_ = json.Unmarshal(raw, &value)
	markers, ok := value["action_markers"].([]any)
	if !ok || len(markers) != 2 {
		t.Fatalf("missing deterministic source evidence: %s", raw)
	}
	first := markers[0].(map[string]any)
	if first["line"] != float64(2) || first["path"] != "service.go" || first["tag"] != "TODO" {
		t.Fatalf("wrong marker: %v", first)
	}
}
