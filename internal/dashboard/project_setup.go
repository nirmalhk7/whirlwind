package dashboard

import (
	"encoding/json"
	"fmt"
	"strings"

	"whirlwind/internal/control"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func operationRunDetails(id string, run ideationRunView) string {
	if run.Kind == "project_setup" {
		return projectSetupRunDetails(id, run)
	}
	return ideationRunDetails(id, run)
}

func projectSetupRunDetails(id string, run ideationRunView) string {
	var evidence struct {
		Phase    string `json:"phase"`
		Progress struct {
			Current   string `json:"current"`
			Completed []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
				Error  string `json:"error,omitempty"`
			} `json:"completed"`
		} `json:"progress"`
	}
	_ = decodeOperationInput(run.Input, &evidence)
	var text strings.Builder
	fmt.Fprintf(&text, "Project setup: %s\nProject: %s\nPhase: %s\nStatus: %s\nStarted: %s\nFinished: %s\nRepository revision: %s\nConfiguration: %s\n", id, run.Subject, projectSetupPhaseLabel(run.Input), run.Stage, recordedRuntimeValue(run.StartedAt), recordedRuntimeValue(run.FinishedAt), recordedRuntimeValue(run.RepositoryRevision), recordedRuntimeValue(run.ConfigurationHash))
	if evidence.Progress.Current != "" {
		fmt.Fprintf(&text, "\nCurrent check: %s\n", evidence.Progress.Current)
	}
	for _, check := range evidence.Progress.Completed {
		fmt.Fprintf(&text, "\n%s: %s\n", check.Name, check.Status)
		if check.Error != "" {
			fmt.Fprintf(&text, "  %s\n", check.Error)
		}
	}
	if run.Error != "" {
		fmt.Fprintf(&text, "\nSetup error: %s\n", run.Error)
	}
	if run.Stage == "failed" {
		text.WriteString("\nPress r in Runs to explicitly retry setup.\n")
	} else if run.Stage == "complete" {
		text.WriteString("\nPress r in Runs to explicitly recheck project setup.\n")
	}
	return text.String()
}

func projectSetupPhaseLabel(input any) string {
	var phase struct {
		Onboarding bool   `json:"onboarding"`
		Phase      string `json:"phase"`
	}
	_ = decodeOperationInput(input, &phase)
	if !phase.Onboarding {
		return "Checking project"
	}
	switch phase.Phase {
	case "catalog":
		return "Recording scanner configuration"
	case "qualify":
		return "Checking project"
	default:
		return "Preparing quality config"
	}
}

func decodeOperationInput(value any, destination any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, destination)
}

func (d *Dashboard) retrySelectedProjectSetup() bool {
	index := d.runs.GetCurrentItem()
	if index < 0 || index >= len(d.runRows) {
		return false
	}
	run := d.runRows[index].operation
	if run == nil || run.Kind != "project_setup" || run.Stage != "failed" && run.Stage != "complete" {
		return false
	}
	recheck := run.Stage == "complete"
	var version int
	for _, record := range d.snapshot["projects"] {
		if record.ID != run.Subject {
			continue
		}
		project, err := store.Decode[model.Project](record)
		wantStatus := "error"
		if recheck {
			wantStatus = "approved"
		}
		if err != nil || project.ID != run.Subject || project.Status != wantStatus || project.Paused {
			d.status.SetText("Whirlwind | project setup cannot be retried or rechecked from current project state")
			return true
		}
		version = record.Version
		break
	}
	if version < 1 {
		d.status.SetText("Whirlwind | project setup state unavailable; refresh before retrying")
		return true
	}
	if !d.hasCommandHandler("Whirlwind | local supervisor unavailable; setup remains failed") {
		return true
	}
	d.submit(control.Request{Operation: "project.setup.retry", ProjectID: run.Subject, ExpectedVersion: version})
	return true
}
