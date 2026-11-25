package workflow

import (
	"testing"

	"whirlwind/internal/verification"
)

func TestProjectSetupProgressRetainsResultsAndRejectsInvalidEvents(t *testing.T) {
	const command = "syntax"
	evidence := projectSetupEvidence{
		Manifest: verification.JobManifest{Commands: []verification.JobCommand{{Name: command}}},
	}
	requireWorkflowSuccess(t, evidence.applyProgress(verification.JobEvent{Type: "started", Index: 0}))
	for _, event := range []verification.JobEvent{
		{Type: "started", Index: -1},
		{Type: "started", Index: 1},
		{Type: "result", Index: 0},
		{Type: "result", Index: 0, Result: &verification.JobResult{Result: verification.Result{Name: "other"}}},
	} {
		if err := evidence.applyProgress(event); err == nil {
			t.Fatalf("invalid verifier event accepted: %+v", event)
		}
	}
	if evidence.Progress.Current != command || len(evidence.Progress.Completed) != 0 {
		t.Fatalf("invalid verifier event changed progress: %+v", evidence.Progress)
	}
	result := verification.JobResult{Result: verification.Result{Name: command, Error: "fixture syntax error"}, Status: "fail"}
	requireWorkflowSuccess(t, evidence.applyProgress(verification.JobEvent{Type: "result", Index: 0, Result: &result}))
	want := projectSetupCheck{Name: command, Status: result.Status, Error: result.Error}
	if evidence.Progress.Current != "" || len(evidence.Progress.Completed) != 1 || evidence.Progress.Completed[0] != want {
		t.Fatalf("completed verifier event changed its outcome: %+v", evidence.Progress)
	}
}
