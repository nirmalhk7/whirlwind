package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestCancelledMutationJournalCannotBeRetried(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	if err := s.Store.Put(ctx, "mutations", "old", map[string]any{"stage": "cancelled", "fingerprint": "same"}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.mutationJournalVersion(ctx, "old", "same"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("cancelled mutation restarted: %v", err)
	}
}

func TestModeCancellationRejectsPendingQuestionAndPermissionReplies(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	task := model.Task{ID: "waiting", Stage: "questions", PlanVersion: 1, Plan: &model.Plan{Questions: []model.Question{{ID: "target", Text: "Which target?"}}}}
	cancellationFixturePut(t, s, "tasks", task.ID, task)
	cancellationFixturePut(t, s, "questions", "native", model.QuestionSet{ID: "native", TaskID: task.ID, Status: "pending", Questions: task.Plan.Questions})
	cancellationFixturePut(t, s, "permissions", "native", map[string]any{"task": task.ID, "decision": "pending", "attempt_id": "old-attempt"})
	if err := s.Store.ActivateMode(ctx, model.AgenticMode); err != nil {
		t.Fatal(err)
	}
	assertCancelledReplyRejected(t, s, "questions", "native", "answers", map[string]any{"answers": map[string]string{"target": "production"}})
	assertCancelledReplyRejected(t, s, "permissions", "native", "decisions", map[string]string{"decision": "allow"})
	assertCancelledTaskActionsRejected(t, s, task.ID)
	if err := s.Engine.Step(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	record, err := s.Store.Get(ctx, "tasks", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := store.Decode[model.Task](record)
	if err != nil || !cancelled.Cancelled() || cancelled.Plan.Questions[0].Answer != "" {
		t.Fatalf("stale response revived cancelled task: %+v %v", cancelled, err)
	}
}

func cancellationFixturePut(t *testing.T, s *Server, kind, id string, value any) {
	t.Helper()
	if err := s.Store.Put(context.Background(), kind, id, value, 0); err != nil {
		t.Fatal(err)
	}
}

func assertCancelledReplyRejected(t *testing.T, s *Server, kind, id, action string, body any) {
	t.Helper()
	before, err := s.Store.Get(context.Background(), kind, id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(before.Data), "cancelled") {
		t.Fatalf("mode transition left %s request pending: %s", kind, before.Data)
	}
	for _, version := range []string{"1", strconv.Itoa(before.Version)} {
		response := workflowModeRequest(t, s, "POST", kind+"/"+id+"/"+action, version, body)
		if response.Code != 409 {
			t.Fatalf("cancelled %s accepted reply: %d %s", kind, response.Code, response.Body)
		}
	}
	after, err := s.Store.Get(context.Background(), kind, id)
	if err != nil || after.Version != before.Version || !bytes.Equal(after.Data, before.Data) {
		t.Fatalf("rejected reply changed %s: %+v %v", kind, after, err)
	}
}

func assertCancelledTaskActionsRejected(t *testing.T, s *Server, id string) {
	t.Helper()
	record, err := s.Store.Get(context.Background(), "tasks", id)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"resume", "adopt", "answers", "approve"} {
		response := workflowModeRequest(t, s, "POST", "tasks/"+id+"/"+action, strconv.Itoa(record.Version), map[string]any{"answers": map[string]string{"target": "production"}})
		if response.Code != 409 || !strings.Contains(response.Body.String(), "task_cancelled") {
			t.Fatalf("cancelled task accepted %s: %d %s", action, response.Code, response.Body)
		}
	}
}

func TestSelectingCancelledLegacyDirectionCreatesFreshTask(t *testing.T) {
	for _, linked := range []bool{false, true} {
		t.Run(strconv.FormatBool(linked), func(t *testing.T) { assertCancelledDirectionReselection(t, linked) })
	}
}

func assertCancelledDirectionReselection(t *testing.T, linked bool) {
	t.Helper()
	s := setup(t)
	ctx := context.Background()
	cancellationFixturePut(t, s, "projects", "p", model.Project{ID: "p", PolicyHash: "confirmed"})
	direction := model.Direction{ID: "legacy", ProjectID: "p", Title: "Preserve objective", Description: "Implement selected scope", Scope: []string{"result.txt"}, Status: "selected", Selected: true}
	old := model.Task{ID: "direction-legacy", ProjectID: "p", Stage: "coding", Worktree: "/preserved-worktree", PlanVersion: 3}
	if linked {
		old.ID = "linked-task"
		direction.TaskID = old.ID
	}
	cancellationFixturePut(t, s, "directions", direction.ID, direction)
	cancellationFixturePut(t, s, "tasks", old.ID, old)
	if err := s.Store.ActivateMode(ctx, model.AgenticMode); err != nil {
		t.Fatal(err)
	}
	before, err := s.Store.Get(ctx, "tasks", old.ID)
	if err != nil {
		t.Fatal(err)
	}
	response := workflowModeRequest(t, s, "POST", "directions/legacy/select", "1", map[string]any{})
	var record model.Record
	if err := json.Unmarshal(response.Body.Bytes(), &record); err != nil || response.Code != 202 {
		t.Fatalf("explicit reselection failed: %d %s %v", response.Code, response.Body, err)
	}
	fresh, err := store.Decode[model.Task](record)
	if err != nil || fresh.ID == old.ID || fresh.Mode != model.AgenticMode || fresh.Stage != "planning" || fresh.Worktree != "" || fresh.PlanVersion != 1 {
		t.Fatalf("selection reused cancelled execution: %+v %v", fresh, err)
	}
	if fresh.Selection == nil || fresh.Selection.Objective != direction.Title || fresh.Selection.PolicyHash != "confirmed" || fresh.Selection.CandidateVersion != 1 {
		t.Fatalf("new task missing fresh authorization: %+v", fresh.Selection)
	}
	after, err := s.Store.Get(ctx, "tasks", old.ID)
	if err != nil || after.Version != before.Version || !bytes.Equal(after.Data, before.Data) {
		t.Fatalf("new selection altered cancelled history: %+v %v", after, err)
	}
	assertDirectionLinksFreshTask(t, s, direction.ID, fresh.ID)
}

func assertDirectionLinksFreshTask(t *testing.T, s *Server, id, taskID string) {
	t.Helper()
	record, err := s.Store.Get(context.Background(), "directions", id)
	if err != nil {
		t.Fatal(err)
	}
	direction, err := store.Decode[model.Direction](record)
	if err != nil || !direction.Selected || direction.TaskID != taskID {
		t.Fatalf("selection did not link fresh task: %+v %v", direction, err)
	}
}
