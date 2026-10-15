package httpapi

import (
	"encoding/json"
	"strings"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/workflow"
)

func (q apiRequest) taskAction() error {
	t, record, err := readCurrent[model.Task](q, "tasks")
	if err != nil {
		return err
	}
	handlers := map[string]func(*model.Task) error{
		"pause": q.pauseTask, "resume": q.resumeTask, "answers": q.answersTask, "approve": q.approveTask,
	}
	handler, ok := handlers[q.action]
	if !ok {
		return &apiError{404, "route_not_found", "Unknown task action"}
	}
	if err := handler(&t); err != nil {
		return err
	}
	audit := model.Audit{Task: t.ID, Source: "local_operator", Action: q.action, Target: t.ID, Version: record.Version, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Evidence: t.Approval}
	if err = q.Store.Put(q.r.Context(), "audit", store.ID(), audit, 0); err != nil {
		return err
	}
	q.save(q.w, q.r, "tasks", q.id, t, record.Version, 200)
	return nil
}

func (q apiRequest) pauseTask(t *model.Task) error {
	if t.Stage == "complete" || t.Stage == "pr_created" {
		return &apiError{409, "invalid_stage", "Published work has no running task to pause"}
	}
	if t.Stage != "paused" {
		t.ResumeStage = t.Stage
	}
	store.AfterCommit(q.r.Context(), func() { q.Engine.Stop(q.id) })
	t.Stage = "paused"
	t.Error = "Paused by user"
	return nil
}

func (q apiRequest) resumeTask(t *model.Task) error {
	if t.Stage != "paused" {
		return &apiError{409, "invalid_stage", "Only paused tasks resume"}
	}
	t.Error = ""
	t.Stage = resumedImplementationStage(t)
	if t.PR != "" {
		return &apiError{409, "invalid_stage", "Review existing published PR in GitHub"}
	}
	if t.ResumeStage != "" && t.ResumeStage != "paused" {
		t.Stage = t.ResumeStage
	} else if t.Plan != nil {
		for _, question := range t.Plan.Questions {
			if question.Answer == "" {
				t.Stage = "questions"
			}
		}
	}
	t.EligibilityOverride = !t.Issue.Eligible()
	t.UnchangedFailures = 0
	t.Recoveries = 0
	return nil
}

func (q apiRequest) answersTask(t *model.Task) error {
	var input struct {
		Answers map[string]string `json:"answers"`
	}
	if t.Stage != "questions" {
		return &apiError{409, "invalid_stage", "Task has no pending questions"}
	}
	if err := json.Unmarshal(q.body, &input); err != nil {
		return err
	}
	for n := range t.Plan.Questions {
		q := &t.Plan.Questions[n]
		if a, ok := input.Answers[q.ID]; ok {
			q.Answer = strings.TrimSpace(a)
		}
	}
	complete := true
	for _, q := range t.Plan.Questions {
		if q.Answer == "" {
			complete = false
		}
	}
	if complete {
		t.Stage = "planning"
	}
	return nil
}

func (q apiRequest) approveTask(t *model.Task) error {
	if t.Stage != "approval" {
		return &apiError{409, "invalid_stage", "Task does not await critical approval"}
	}
	if t.ReviewID == "" || t.VerificationID == "" {
		return &apiError{409, "evidence_required", "Refresh verification and review before approval"}
	}
	project, err := q.Engine.Project(q.r.Context(), t.ProjectID)
	if err != nil {
		return err
	}
	proof, err := workflow.PublicationEvidence(q.r.Context(), q.Store, *t, project.PolicyHash)
	if err != nil {
		return &apiError{409, "evidence_required", err.Error()}
	}
	t.Approval = &proof
	t.Approved = true
	t.Stage = "publishing"
	return nil
}

func resumedImplementationStage(t *model.Task) string {
	if t.Plan == nil {
		return "planning"
	}
	if t.Worktree == "" {
		return "coding"
	}
	for _, unit := range t.Plan.Units {
		if unit.Stage != "integrated" {
			return "coding"
		}
	}
	return "repair"
}
