package workflow

import (
	"context"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (a *agentAttempt) questions(ctx context.Context, questions []model.Question) (map[string]string, error) {
	set := model.QuestionSet{ID: store.ID(), TaskID: a.task.ID, AttemptID: a.id, Provider: a.chosen.ID, Questions: questions, Status: "pending", PlanVersion: a.task.PlanVersion}
	notification := model.Notification{ID: store.ID(), TaskID: a.task.ID, Kind: "question", Message: "Agent clarification awaits answer: " + set.ID}
	if err := a.Store.Write(ctx, []store.Change{{Kind: "questions", ID: set.ID, Value: set}, {Kind: "inbox", ID: notification.ID, Value: notification}}, nil); err != nil {
		return nil, a.persistenceError(err)
	}
	defer a.cancelQuestions(ctx, set.ID)
	value, err := awaitResource(ctx, a.Store, "questions", set.ID, func(value model.QuestionSet) bool { return value.Status == "answered" })
	return value.Answers, err
}
func (a *agentAttempt) cancelQuestions(ctx context.Context, id string) {
	if ctx.Err() == nil {
		return
	}
	record, err := a.Store.Get(context.Background(), "questions", id)
	if err != nil {
		return
	}
	pending, _ := store.Decode[model.QuestionSet](record)
	if pending.Status == "pending" {
		pending.Status = "cancelled"
		_ = a.Store.Put(context.Background(), "questions", id, pending, record.Version)
	}
}
