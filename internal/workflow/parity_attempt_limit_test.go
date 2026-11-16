package workflow

import (
	"encoding/json"
	"testing"
)

func TestParityDirectionCandidateDefaultIsFive(t *testing.T) {
	data, err := json.Marshal(DefaultSettings().Ideation)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	if settings["max_attempts_per_direction"] != float64(5) {
		t.Fatalf("missing five-candidate direction setting: %s", data)
	}
}
