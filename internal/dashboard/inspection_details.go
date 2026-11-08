package dashboard

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

const listBulletFormat = "- %s\n"

func (d *Dashboard) selectPlan(index int, _, _ string, _ rune) {
	if d.updating || index < 0 || index >= len(d.planRows) {
		return
	}
	plan := d.planRows[index]
	d.planID = plan.ID
	var text strings.Builder
	fmt.Fprintf(&text, "%s\n\nState: %s | Plan version: %d\nDelivery branch: %s\nWorktree: %s\nReviewed commit: %s\nStored task score (raw; not acceptance evidence): %g\n\n", planTitle(plan), plan.Stage, plan.PlanVersion, plan.Branch, plan.Worktree, plan.ReviewedCommit, plan.Score)
	if plan.Error != "" {
		fmt.Fprintf(&text, "Error: %s\n\n", plan.Error)
	}
	d.writeQuestions(&text, plan.ID)
	d.writeAttempts(&text, plan.ID)
	if plan.Plan != nil {
		fmt.Fprintf(&text, "Approved/proposed plan\n%s\n\nAcceptance criteria\n", plan.Plan.Specification)
		for _, criterion := range plan.Plan.AcceptanceCriteria {
			fmt.Fprintf(&text, listBulletFormat, criterion)
		}
	}
	d.writeEvidence(&text, plan)
	d.writeReviewAdherence(&text, plan)
	d.writeDelivery(&text, plan)
	d.detail.SetText(text.String())
}

func (d *Dashboard) writeReviewAdherence(text *strings.Builder, task model.Task) {
	review, ok := d.currentReview(task)
	if !ok {
		return
	}
	fmt.Fprintf(text, "\nComputed adherence: %g%% (saved review %s)\n", review.Adherence.Score, task.ReviewID)
	for _, result := range review.Adherence.Results {
		if result.Status == "pass" {
			continue
		}
		fmt.Fprintf(text, "Unmet requirement: %s\n", result.ID)
	}
}

func (d *Dashboard) currentReview(task model.Task) (model.Review, bool) {
	if task.ReviewID == "" || task.VerificationID == "" || task.ReviewedCommit == "" {
		return model.Review{}, false
	}
	policyHash, ok := d.projectPolicyHash(task.ProjectID)
	if !ok {
		return model.Review{}, false
	}
	for _, record := range d.snapshot["reviews"] {
		if record.ID != task.ReviewID {
			continue
		}
		review, err := store.Decode[model.Review](record)
		if err != nil || !reviewAdherenceMatchesTask(review, task, policyHash) {
			return model.Review{}, false
		}
		return review, true
	}
	return model.Review{}, false
}

func (d *Dashboard) projectPolicyHash(projectID string) (string, bool) {
	for _, record := range d.snapshot["projects"] {
		if record.ID != projectID {
			continue
		}
		project, err := store.Decode[model.Project](record)
		return project.PolicyHash, err == nil && project.PolicyHash != ""
	}
	return "", false
}

func reviewAdherenceMatchesTask(review model.Review, task model.Task, policyHash string) bool {
	if review.Adherence == nil || review.Adherence.Total == 0 || review.Provenance == nil {
		return false
	}
	return reviewProvenanceMatchesTask(*review.Provenance, task, policyHash) && reviewProvenanceMatchesTask(review.Adherence.Provenance, task, policyHash)
}

func reviewProvenanceMatchesTask(provenance model.Provenance, task model.Task, policyHash string) bool {
	return provenance.Task == task.ID && provenance.PlanVersion == task.PlanVersion && provenance.Head == task.ReviewedCommit && provenance.PolicyHash == policyHash && provenance.ReviewID == task.ReviewID && provenance.VerificationID == task.VerificationID
}

func (d *Dashboard) writeQuestions(text *strings.Builder, planID string) {
	for _, record := range d.snapshot["questions"] {
		questions, err := store.Decode[model.QuestionSet](record)
		if err == nil && questions.TaskID == planID && questions.Status == "pending" {
			text.WriteString("Pending clarification\n")
			for _, question := range questions.Questions {
				fmt.Fprintf(text, listBulletFormat, question.Text)
			}
			text.WriteByte('\n')
		}
	}
}

func (d *Dashboard) writeAttempts(text *strings.Builder, planID string) {
	text.WriteString("Agent attempts\n")
	now := time.Now()
	for _, record := range d.snapshot["sessions"] {
		attempt, err := store.Decode[model.Attempt](record)
		if err != nil || attempt.Task != planID {
			continue
		}
		remaining := "inactive"
		heartbeat := "inactive"
		if activeAttempt(attempt) {
			remaining = remainingDeadline(attempt.DeadlineAt)
			heartbeat = workerHeartbeatStatus(attempt, now)
		}
		previous := attempt.PreviousAttemptID
		if previous == "" {
			previous = "none recorded"
		}
		fmt.Fprintf(text, "%s | %s | ACP agent: %s | model: %s | thinking: %s | queue entry: %s | restarts: %d | remaining: %s | heartbeat: %s | process: %s | sandbox: %s (%s) | previous attempt: %s\n", attempt.Role, attempt.Status, attempt.Provider, attemptModelDisplay(attempt), attemptThinkingDisplay(attempt), queueEntryDisplay(attempt), attempt.RestartCount, remaining, heartbeat, recordedRuntimeValue(attempt.ProcessState), recordedRuntimeValue(attempt.SandboxHealth), recordedRuntimeValue(attempt.SandboxPhase), previous)
		if attempt.Error != "" {
			fmt.Fprintf(text, "  %s\n", attempt.Error)
		}
	}
	text.WriteByte('\n')
}

func activeAttempt(attempt model.Attempt) bool {
	return !attempt.Finished && attempt.Status == "running"
}

func attemptModelDisplay(attempt model.Attempt) string {
	if attempt.ResolvedModel != "" {
		return "effective " + attempt.ResolvedModel
	}
	if attempt.Model != "" {
		return "configured " + attempt.Model + " (effective not recorded)"
	}
	return "unknown (not recorded)"
}

func attemptThinkingDisplay(attempt model.Attempt) string {
	if attempt.EffectiveThinking != nil {
		return "effective " + thinkingDisplay(attempt.EffectiveThinking)
	}
	if attempt.Thinking != nil {
		return "configured " + thinkingDisplay(attempt.Thinking) + " (effective not recorded)"
	}
	return "not recorded"
}

func thinkingDisplay(choice *model.ThinkingChoice) string {
	if choice.ConfigID == "" {
		return choice.Value
	}
	return choice.ConfigID + "=" + choice.Value
}

func queueEntryDisplay(attempt model.Attempt) string {
	if attempt.QueuePosition < 1 || attempt.QueueLength < attempt.QueuePosition {
		return "unknown (not recorded)"
	}
	return fmt.Sprintf("%d/%d", attempt.QueuePosition, attempt.QueueLength)
}

func workerHeartbeatStatus(attempt model.Attempt, now time.Time) string {
	if attempt.WorkerHeartbeatAt == "" || attempt.LeaseExpiresAt == "" {
		return "unknown (heartbeat or lease not recorded)"
	}
	heartbeat, err := time.Parse(time.RFC3339Nano, attempt.WorkerHeartbeatAt)
	if err != nil || heartbeat.After(now) {
		return "unknown (invalid heartbeat timestamp)"
	}
	lease, err := time.Parse(time.RFC3339Nano, attempt.LeaseExpiresAt)
	if err != nil {
		return "unknown (invalid lease timestamp)"
	}
	if !lease.After(now) {
		return "expired"
	}
	if heartbeat.After(lease) {
		return "unknown (lease predates heartbeat)"
	}
	return fmt.Sprintf("lease valid (last beat %s ago)", now.Sub(heartbeat).Round(time.Second))
}

func recordedRuntimeValue(value string) string {
	if value == "" {
		return "unknown (not recorded)"
	}
	return value
}

func (d *Dashboard) writeEvidence(text *strings.Builder, plan model.Task) {
	for _, kind := range []string{"verification", "reviews", "permissions"} {
		for _, record := range d.snapshot[kind] {
			var value map[string]any
			if json.Unmarshal(record.Data, &value) != nil {
				continue
			}
			if value["task"] != plan.ID && value["task_id"] != plan.ID && record.ID != plan.VerificationID && record.ID != plan.ReviewID {
				continue
			}
			pretty, _ := json.MarshalIndent(value, "", "  ")
			fmt.Fprintf(text, "\n%s evidence (%s)\n%s\n", kind, record.ID, pretty)
		}
	}
}

func remainingDeadline(value string) string {
	if value == "" {
		return "unknown (no persisted deadline)"
	}
	deadline, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "unknown (invalid persisted deadline)"
	}
	remaining := time.Until(deadline).Round(time.Second)
	if remaining <= 0 {
		return "expired"
	}
	return remaining.String()
}

func (d *Dashboard) writeDelivery(text *strings.Builder, plan model.Task) {
	if plan.Delivery == nil {
		return
	}
	delivery := plan.Delivery
	fmt.Fprintf(text, "\nLocal delivery\nStage: %s | branch: %s | commit: %s\n", delivery.Stage, delivery.Branch, delivery.ReviewedCommit)
	if delivery.Adherence != nil {
		fmt.Fprintf(text, "Computed adherence: %g%%\n", delivery.Adherence.Score)
	} else {
		text.WriteString("Computed adherence: not recorded\n")
	}
	if len(delivery.UnmetRequirements) > 0 {
		text.WriteString("Unmet requirement IDs\n")
		for _, id := range delivery.UnmetRequirements {
			fmt.Fprintf(text, listBulletFormat, id)
		}
	}
	if delivery.Diff != "" {
		const detailLimit = 256 * 1024
		diff := delivery.Diff
		if len(diff) > detailLimit {
			diff = diff[:detailLimit] + "\n[diff display truncated; stored local delivery is unchanged]"
		}
		fmt.Fprintf(text, "\nReviewed local diff\n%s\n", diff)
	}
}
