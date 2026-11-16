package control

import (
	"context"
	"encoding/json"
	"whirlwind/internal/model"
)

func enrichIdeationProgress(ctx context.Context, records []model.Record, read func(context.Context, string) ([]model.IdeationDirectionProgress, error)) error {
	for n := range records {
		var operation model.Operation
		if err := json.Unmarshal(records[n].Data, &operation); err != nil {
			return err
		}
		if operation.Kind != "ideation" {
			continue
		}
		progress, err := read(ctx, records[n].ID)
		if err != nil {
			return err
		}
		var value map[string]json.RawMessage
		if err := json.Unmarshal(records[n].Data, &value); err != nil {
			return err
		}
		value["ideation_directions"], err = json.Marshal(progress)
		if err != nil {
			return err
		}
		records[n].Data, err = json.Marshal(value)
		if err != nil {
			return err
		}
	}
	return nil
}
