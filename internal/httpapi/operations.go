package httpapi

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (q apiRequest) operation(kind, subject string, input any) model.Operation {
	return model.Operation{Kind: kind, Subject: subject, Input: input, Stage: "running", Owner: q.Engine.Owner(), StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
}
func (q apiRequest) finishOperation(id string, value any) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := q.persistOperationCompletion(ctx, id, value); err != nil {
		log.Printf("persist operation %s completion: %v", id, err)
	}
}

func (q apiRequest) persistOperationCompletion(ctx context.Context, id string, value any) error {
	record, err := q.Store.Get(ctx, "operations", id)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	result, err := mergeOperationCompletion(record.Data, encoded)
	if err != nil {
		return err
	}
	return q.Store.Put(ctx, "operations", id, result, record.Version)
}

func mergeOperationCompletion(currentData, resultData []byte) (map[string]json.RawMessage, error) {
	var current, result map[string]json.RawMessage
	if err := json.Unmarshal(resultData, &result); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(currentData, &current); err != nil {
		return nil, err
	}
	if string(current["stage"]) != `"running"` || string(current["owner"]) != string(result["owner"]) || string(current["kind"]) != string(result["kind"]) || string(current["subject"]) != string(result["subject"]) {
		return nil, store.ErrConflict
	}
	for _, key := range []string{"repository_revision", "configuration_hash"} {
		if pinned, ok := current[key]; ok {
			result[key] = pinned
		}
	}
	return result, nil
}
