package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

type pendingPermission struct {
	ID          string          `json:"id"`
	Task        string          `json:"task"`
	AttemptID   string          `json:"attempt_id"`
	Provider    string          `json:"provider"`
	Decision    string          `json:"decision"`
	Request     json.RawMessage `json:"request"`
	PlanVersion int             `json:"plan_version"`
	ConsumedBy  string          `json:"consumed_by_attempt,omitempty"`
}

func (e *Engine) AnswerQuestionSet(ctx context.Context, questionID string, expectedVersion int, answers map[string]string, changesSpecification bool) error {
	return e.AnswerQuestionSetWithReceipt(ctx, questionID, expectedVersion, answers, changesSpecification, nil)
}

func (e *Engine) AnswerQuestionSetWithReceipt(ctx context.Context, questionID string, expectedVersion int, answers map[string]string, changesSpecification bool, receipt *store.Receipt) error {
	questionRecord, err := e.Store.Get(ctx, "questions", questionID)
	if err != nil {
		return err
	}
	if questionRecord.Version != expectedVersion {
		return store.ErrConflict
	}
	set, err := store.Decode[model.QuestionSet](questionRecord)
	if err != nil {
		return err
	}
	if set.Status != "pending" || set.ID != questionID || set.TaskID == "" || set.AttemptID == "" {
		return store.ErrConflict
	}
	if err := validateHumanAnswers(set, answers); err != nil {
		return err
	}
	taskRecord, task, attempt, stage, err := e.pendingDecisionOwner(ctx, set.TaskID, set.AttemptID, set.PlanVersion)
	if err != nil {
		return err
	}
	set.Answers, set.Status = answers, "answered"
	task.Wait = nil
	task.ResumeStage = ""
	task.Error = ""
	if changesSpecification {
		task.PlanVersion++
		task.PlanDocument, task.Plan = nil, nil
		task.Approval = nil
		task.ChecksPassed, task.Approved, task.Score = false, false, 0
		task.VerificationID, task.ReviewID, task.ReviewedCommit = "", "", ""
		task.Stage = "planning"
		task.Handoff.NextAction = "Revise the plan for the answered specification change and obtain human approval before execution."
		appendScopeChangeAnswers(&task, set)
	} else {
		task.Stage = stage
		task.Handoff.NextAction = "Resume the approved plan at its original " + attempt.Role + " stage with a fresh attempt."
	}
	return e.commitHumanDecision(ctx, questionRecord, "questions", set, taskRecord, task, attempt, "answer_questions", questionID, expectedVersion, receipt)
}

func (e *Engine) DecidePermission(ctx context.Context, permissionID string, expectedVersion int, decision string) error {
	return e.DecidePermissionWithReceipt(ctx, permissionID, expectedVersion, decision, nil)
}

func (e *Engine) DecidePermissionWithReceipt(ctx context.Context, permissionID string, expectedVersion int, decision string, receipt *store.Receipt) error {
	if decision != "allow" && decision != "deny" {
		return errors.New("permission decision must be allow or deny")
	}
	permissionRecord, err := e.Store.Get(ctx, "permissions", permissionID)
	if err != nil {
		return err
	}
	if permissionRecord.Version != expectedVersion {
		return store.ErrConflict
	}
	var permission pendingPermission
	if err := json.Unmarshal(permissionRecord.Data, &permission); err != nil {
		return err
	}
	if permission.ID != permissionID || permission.Decision != "pending" || permission.Task == "" || permission.AttemptID == "" {
		return store.ErrConflict
	}
	taskRecord, task, attempt, stage, err := e.pendingDecisionOwner(ctx, permission.Task, permission.AttemptID, permission.PlanVersion)
	if err != nil {
		return err
	}
	permission.Decision = decision
	task.Wait = nil
	task.ResumeStage = ""
	task.Error = ""
	task.Stage = stage
	task.Handoff.NextAction = "Resume the approved plan at its original " + attempt.Role + " stage with a fresh attempt."
	return e.commitHumanDecision(ctx, permissionRecord, "permissions", permission, taskRecord, task, attempt, "permission_"+decision, permissionID, expectedVersion, receipt)
}

func validateHumanAnswers(set model.QuestionSet, answers map[string]string) error {
	if len(set.Questions) == 0 {
		return errors.New("pending question set is empty")
	}
	for _, question := range set.Questions {
		if strings.TrimSpace(answers[question.ID]) == "" {
			return fmt.Errorf("answer every question; missing %q", question.ID)
		}
	}
	return nil
}

func (e *Engine) pendingDecisionOwner(ctx context.Context, taskID, attemptID string, planVersion int) (model.Record, model.Task, model.Attempt, string, error) {
	taskRecord, err := e.Store.Get(ctx, "tasks", taskID)
	if err != nil {
		return model.Record{}, model.Task{}, model.Attempt{}, "", err
	}
	task, err := store.Decode[model.Task](taskRecord)
	if err != nil {
		return taskRecord, task, model.Attempt{}, "", err
	}
	if task.Stage != "paused" || task.Wait == nil || task.Wait.Kind != "human_decision" || task.PlanVersion != planVersion || task.Wait.PlanVersion != planVersion || task.ResumeStage == "" || task.Wait.ResumeStage != task.ResumeStage {
		return taskRecord, task, model.Attempt{}, "", store.ErrConflict
	}
	if task.Wait.AttemptID != "" && task.Wait.AttemptID != attemptID {
		return taskRecord, task, model.Attempt{}, "", store.ErrConflict
	}
	if task.Wait.AttemptID == "" {
		matches, matchErr := uniquePendingDecisionAttempt(ctx, e.Store, task.ID, planVersion, attemptID)
		if matchErr != nil {
			return taskRecord, task, model.Attempt{}, "", matchErr
		}
		if !matches {
			return taskRecord, task, model.Attempt{}, "", store.ErrConflict
		}
	}
	attemptRecord, err := e.Store.Get(ctx, "sessions", attemptID)
	if err != nil {
		return taskRecord, task, model.Attempt{}, "", err
	}
	attempt, err := store.Decode[model.Attempt](attemptRecord)
	if err != nil {
		return taskRecord, task, attempt, "", err
	}
	if attempt.AttemptID != attemptID || attempt.Task != task.ID || attempt.PlanVersion != planVersion || !attempt.Finished {
		return taskRecord, task, attempt, "", store.ErrConflict
	}
	if !roleMayResumeStage(attempt.Role, task.ResumeStage) {
		return taskRecord, task, attempt, "", store.ErrConflict
	}
	return taskRecord, task, attempt, task.ResumeStage, nil
}

func uniquePendingDecisionAttempt(ctx context.Context, database *store.Store, taskID string, planVersion int, attemptID string) (bool, error) {
	attempts := map[string]bool{}
	for _, kind := range []string{"questions", "permissions"} {
		records, err := database.List(ctx, kind)
		if err != nil {
			return false, err
		}
		for _, record := range records {
			var owner struct {
				ID          string `json:"id"`
				TaskID      string `json:"task_id"`
				Task        string `json:"task"`
				AttemptID   string `json:"attempt_id"`
				PlanVersion int    `json:"plan_version"`
				Status      string `json:"status"`
				Decision    string `json:"decision"`
			}
			if err := json.Unmarshal(record.Data, &owner); err != nil {
				return false, err
			}
			ownedByTask := owner.TaskID == taskID || owner.Task == taskID
			isPending := owner.Status == "pending" || owner.Decision == "pending"
			if ownedByTask && isPending && owner.PlanVersion == planVersion && owner.AttemptID != "" {
				attempts[owner.AttemptID] = true
			}
		}
	}
	return len(attempts) == 1 && attempts[attemptID], nil
}

func latestPendingDecisionAttempt(ctx context.Context, database *store.Store, taskID string, planVersion int) (string, error) {
	pending := map[string]bool{}
	for _, kind := range []string{"questions", "permissions"} {
		records, err := database.List(ctx, kind)
		if err != nil {
			return "", err
		}
		for _, record := range records {
			var owner struct {
				TaskID      string `json:"task_id"`
				Task        string `json:"task"`
				AttemptID   string `json:"attempt_id"`
				PlanVersion int    `json:"plan_version"`
				Status      string `json:"status"`
				Decision    string `json:"decision"`
			}
			if err := json.Unmarshal(record.Data, &owner); err != nil {
				return "", err
			}
			ownedByTask := owner.TaskID == taskID || owner.Task == taskID
			isPending := owner.Status == "pending" || owner.Decision == "pending"
			if ownedByTask && isPending && owner.PlanVersion == planVersion && owner.AttemptID != "" {
				pending[owner.AttemptID] = true
			}
		}
	}
	records, err := database.List(ctx, "sessions")
	if err != nil {
		return "", err
	}
	var latest model.Attempt
	var latestOrder int64
	for _, record := range records {
		attempt, err := store.Decode[model.Attempt](record)
		if err != nil {
			return "", err
		}
		if attempt.Task == taskID && attempt.PlanVersion == planVersion && record.CreatedOrder > latestOrder {
			latest, latestOrder = attempt, record.CreatedOrder
		}
	}
	if latest.AttemptID == "" || !latest.Finished || latest.Status != "paused" || !pending[latest.AttemptID] {
		return "", store.ErrConflict
	}
	return latest.AttemptID, nil
}

func roleMayResumeStage(role, stage string) bool {
	switch role {
	case "plan":
		return stage == "planning"
	case "code":
		return stage == "coding" || stage == "repair"
	case "review":
		return stage == "review"
	case "finalizer":
		return stage == "submitting"
	case "submit":
		return stage == "publishing" || stage == "submitting"
	default:
		return false
	}
}

func appendScopeChangeAnswers(task *model.Task, set model.QuestionSet) {
	var section strings.Builder
	section.WriteString("\n\nSpecification-changing clarification responses:\n")
	for _, question := range set.Questions {
		answer := set.Answers[question.ID]
		if question.Secret || answer == "" {
			continue
		}
		fmt.Fprintf(&section, "- %s: %s\n", question.Text, answer)
	}
	task.Issue.Body += section.String()
}

func (e *Engine) commitHumanDecision(ctx context.Context, decisionRecord model.Record, kind string, value any, taskRecord model.Record, task model.Task, attempt model.Attempt, action, target string, version int, receipt *store.Receipt) error {
	notices, err := e.humanDecisionNotices(ctx, task.ID, target)
	if err != nil {
		return err
	}
	changes := []store.Change{
		{Kind: kind, ID: target, Value: value, Expected: decisionRecord.Version},
		{Kind: "tasks", ID: task.ID, Value: task, Expected: taskRecord.Version},
	}
	changes = append(changes, notices...)
	audit := model.Audit{Task: task.ID, AttemptID: attempt.AttemptID, Source: "local_operator", Action: action, Target: target, Version: version, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	changes = append(changes, store.Change{Kind: "audit", ID: store.ID(), Value: audit})
	return e.Store.Write(ctx, changes, receipt)
}

func (e *Engine) humanDecisionNotices(ctx context.Context, taskID, decisionID string) ([]store.Change, error) {
	records, err := e.Store.List(ctx, "inbox")
	if err != nil {
		return nil, err
	}
	changes := make([]store.Change, 0, 1)
	for _, record := range records {
		notice, decodeErr := store.Decode[model.Notification](record)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if notice.TaskID == taskID && !notice.Resolved && strings.HasSuffix(notice.Message, decisionID) {
			notice.Resolved = true
			changes = append(changes, store.Change{Kind: "inbox", ID: record.ID, Value: notice, Expected: record.Version})
		}
	}
	return changes, nil
}

func permissionRequestMatches(raw json.RawMessage, perm any) bool {
	encoded, err := json.Marshal(perm)
	if err != nil {
		return false
	}
	safe, err := store.SafeJSON(perm)
	if err != nil || !sameJSONValue(encoded, safe) {
		return false
	}
	return sameJSONValue(raw, safe)
}

func sameJSONValue(left, right []byte) bool {
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	leftJSON, leftErr := json.Marshal(leftValue)
	rightJSON, rightErr := json.Marshal(rightValue)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}
