package workflow

import (
	"context"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (e *Engine) WorkflowConfiguration(ctx context.Context) model.WorkflowConfig {
	state := model.WorkflowConfig{ActiveMode: model.PersonalMode, ConfiguredMode: model.PersonalMode}
	if record, err := e.Store.Get(ctx, "workflow_config", "global"); err == nil {
		if v, err := store.Decode[model.WorkflowConfig](record); err == nil {
			state = v
		}
	}
	state.ActiveMode = model.EffectiveMode(state.ActiveMode)
	return state
}
func (e *Engine) Agentic(ctx context.Context) bool {
	return e.WorkflowConfiguration(ctx).ActiveMode == model.AgenticMode
}
