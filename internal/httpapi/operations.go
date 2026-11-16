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
	record, err := q.Store.Get(ctx, "operations", id)
	if err == nil {
		var current, result map[string]json.RawMessage
		var encoded []byte
		encoded, err = json.Marshal(value)
		if err == nil {
			err = json.Unmarshal(encoded, &result)
		}
		if err == nil {
			err = json.Unmarshal(record.Data, &current)
		}
		if err == nil {
			if string(current["stage"]) != `"running"` || string(current["owner"]) != string(result["owner"]) || string(current["kind"]) != string(result["kind"]) || string(current["subject"]) != string(result["subject"]) {
				err = store.ErrConflict
			} else {
				for _, key := range []string{"repository_revision", "configuration_hash"} {
					if pinned, ok := current[key]; ok {
						result[key] = pinned
					}
				}
				err = q.Store.Put(ctx, "operations", id, result, record.Version)
			}
		}
	}
	if err != nil {
		log.Printf("persist operation %s completion: %v", id, err)
	}
}
