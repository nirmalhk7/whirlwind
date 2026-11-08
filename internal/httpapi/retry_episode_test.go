package httpapi

import (
	"encoding/json"
	"testing"
	"whirlwind/internal/model"
)

func TestHumanResumeStartsANewRecoveryGeneration(t *testing.T) {
	task := model.Task{ID: "task", Stage: "paused", ResumeStage: "coding", Selection: &model.SelectionAuthorization{CandidateID: "candidate", CandidateVersion: 1, SelectedAt: "2026-11-06T00:00:00Z"}}
	if err := (apiRequest{}).resumeTask(&task); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved["recovery_generation"] != float64(1) {
		t.Fatalf("human retry generation = %v, want 1", saved["recovery_generation"])
	}
}
