package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"whirlwind/internal/model"
)

// ActivateMode runs under the exclusive startup lock after registered children are reaped.
// Activation and cancellation share one transaction; no partial transition can resume.
func (s *Store) ActivateMode(ctx context.Context, mode string) error {
	mode = model.EffectiveMode(mode)
	if err := model.ValidateMode(mode); err != nil {
		return err
	}
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		state, version, err := readWorkflowMode(ctx, tx)
		if err != nil {
			return err
		}
		if model.EffectiveMode(state.ActiveMode) != mode {
			if err = cancelModeWork(ctx, tx); err != nil {
				return err
			}
		}
		state.ActiveMode = mode
		state.ConfiguredMode = mode
		state.RestartRequired = false
		return put(ctx, tx, "workflow_config", "global", state, version)
	})
}

func readWorkflowMode(ctx context.Context, tx *sql.Tx) (model.WorkflowConfig, int, error) {
	var data []byte
	var version int
	state := model.WorkflowConfig{ActiveMode: model.PersonalMode}
	err := tx.QueryRowContext(ctx, "SELECT data,version FROM resources WHERE kind='workflow_config' AND id='global'").Scan(&data, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return state, 0, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &state)
	}
	return state, version, err
}
