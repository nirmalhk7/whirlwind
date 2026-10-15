package httpapi

import (
	"context"
	"log"
	"time"
	"whirlwind/internal/model"
)

func (q apiRequest) operation(kind, subject string, input any) model.Operation {
	return model.Operation{Kind: kind, Subject: subject, Input: input, Stage: "running", Owner: q.Engine.Owner(), StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
}
func (q apiRequest) finishOperation(id string, value any) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	record, err := q.Store.Get(ctx, "operations", id)
	if err == nil {
		err = q.Store.Put(ctx, "operations", id, value, record.Version)
	}
	if err != nil {
		log.Printf("persist operation %s completion: %v", id, err)
	}
}
