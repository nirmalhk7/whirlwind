package workflow

import (
	"context"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (a *agentAttempt) questions(ctx context.Context, questions []model.Question) (map[string]string, error) {
	set := model.QuestionSet{ID: store.ID(), TaskID: a.task.ID, AttemptID: a.id, Provider: a.chosen.ID, Questions: questions, Status: "pending", PlanVersion: a.task.PlanVersion}
	notification := model.Notification{ID: store.ID(), TaskID: a.task.ID, Kind: "question", Message: "Agent clarification awaits answer: " + set.ID}
	persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Store.Write(persistCtx, []store.Change{{Kind: "questions", ID: set.ID, Value: set}, {Kind: "inbox", ID: notification.ID, Value: notification}}, nil); err != nil {
		return nil, a.persistenceError(err)
	}
	return nil, ErrHumanDecisionPending
}
