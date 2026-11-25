package dashboard

import (
	"fmt"
	"strings"
)

func ideationRunDetails(id string, run ideationRunView) string {
	var text strings.Builder
	fmt.Fprintf(&text, "Ideation run: %s\nProject: %s\nStatus: %s\nStarted: %s\nFinished: %s\nRepository snapshot: %s\nConfiguration: %s\nCurrent uniqueness policy: Deferred\n", id, run.Subject, run.Stage, recordedRuntimeValue(run.StartedAt), recordedRuntimeValue(run.FinishedAt), recordedRuntimeValue(run.RepositoryRevision), recordedRuntimeValue(run.ConfigurationHash))
	if run.RegistrationIdeation() {
		text.WriteString("Trigger: project registration (automatic)\n")
	}
	for _, direction := range run.Directions {
		fmt.Fprintf(&text, "\n%s: %d/5 · %s\n", direction.Direction, direction.CandidateCount, direction.Status)
		if direction.IdeaID != "" {
			fmt.Fprintf(&text, "Accepted idea: %s\n", direction.IdeaID)
		}
		if direction.ErrorReason != "" {
			fmt.Fprintf(&text, "Reason: %s\n", direction.ErrorReason)
		}
	}
	if run.Error != "" {
		label := "Run error"
		if run.RegistrationIdeation() && run.Stage == "queued" {
			label = "Not started"
		}
		fmt.Fprintf(&text, "\n%s: %s\n", label, run.Error)
	}
	return text.String()
}
