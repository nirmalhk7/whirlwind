package dashboard

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const decisionDetailsMarker = "\n--- Saved details (do not edit below this line) ---\n"

func (d *Dashboard) editDecision(details string, actions []string) (string, string, bool) {
	if !strings.HasSuffix(details, "\n") {
		details += "\n"
	}
	instructions := "# Record your decision\n# Change Decision below to " + strings.Join(actions, " | ") + " | cancel.\n# Reason is rejection feedback for proposals; revision/review feedback for plans.\n# Save and quit with :wq to submit. Unchanged/cancel submits nothing. :cq aborts.\nDecision: cancel\nReason:\n"
	original := instructions + decisionDetailsMarker + details
	file, err := os.CreateTemp("", "whirlwind-decision-*.md")
	if err != nil {
		d.status.SetText("Whirlwind | cannot open decision: " + err.Error())
		return "", "", false
	}
	defer os.Remove(file.Name())
	_, err = file.WriteString(original)
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		if !d.Suspend(func() {
			command := exec.Command("vim", "-n", "-c", "setlocal filetype=markdown", "-c", "syntax enable", "--", file.Name())
			command.Stdin = os.Stdin
			command.Stdout = os.Stdout
			command.Stderr = os.Stderr
			err = command.Run()
		}) {
			err = fmt.Errorf("terminal cannot be suspended")
		}
	}
	if err != nil {
		d.status.SetText("Whirlwind | decision cancelled; Vim exited without a successful review")
		return "", "", false
	}
	edited, err := os.ReadFile(file.Name())
	if err != nil {
		d.status.SetText("Whirlwind | cannot read decision: " + err.Error())
		return "", "", false
	}
	action, reason, err := parseVimDecision(string(edited), details, actions)
	if err != nil {
		d.status.SetText("Whirlwind | invalid decision: " + err.Error() + "; reopen review to try again")
		return "", "", false
	}
	if action == "cancel" {
		d.status.SetText("Whirlwind | decision cancelled; saved proposal/plan unchanged")
	}
	return action, reason, true
}

func parseVimDecision(document, details string, actions []string) (string, string, error) {
	header, saved, ok := strings.Cut(document, decisionDetailsMarker)
	if !ok || saved != details {
		return "", "", fmt.Errorf("saved details changed; edit only Decision and Reason")
	}
	action := ""
	var reason strings.Builder
	inReason := false
	for _, line := range strings.Split(header, "\n") {
		if inReason {
			reason.WriteString(line)
			reason.WriteByte('\n')
			continue
		}
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "Decision:") && action == "" {
			action = strings.TrimSpace(strings.TrimPrefix(line, "Decision:"))
			continue
		}
		if line == "Reason:" {
			inReason = true
			continue
		}
		return "", "", fmt.Errorf("expected one Decision line followed by Reason")
	}
	if !inReason || action == "" {
		return "", "", fmt.Errorf("Decision and Reason fields required")
	}
	allowed := action == "cancel"
	for _, value := range actions {
		allowed = allowed || action == value
	}
	if !allowed {
		return "", "", fmt.Errorf("choose %s or cancel", strings.Join(actions, ", "))
	}
	return action, strings.TrimSpace(reason.String()), nil
}
