package store

import (
	"encoding/json"
	"testing"
	"time"

	"whirlwind/internal/model"
)

func TestInitialProjectSetupMarksAutomaticOnboarding(t *testing.T) {
	changes := InitialProjectWork("project", time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC))
	var setup model.Operation
	for _, change := range changes {
		if change.Kind == "operations" {
			encoded, err := json.Marshal(change.Value)
			if err != nil {
				t.Fatal(err)
			}
			var candidate model.Operation
			if err := json.Unmarshal(encoded, &candidate); err != nil {
				t.Fatal(err)
			}
			if candidate.Kind == "project_setup" {
				setup = candidate
			}
		}
	}
	var input map[string]any
	encoded, err := json.Marshal(setup.Input)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &input); err != nil {
		t.Fatal(err)
	}
	if input["onboarding"] != true {
		t.Fatalf("registered project setup must opt into automatic external quality onboarding: %#v", input)
	}
}
