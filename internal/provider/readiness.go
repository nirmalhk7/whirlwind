package provider

import "context"

// CheckReadiness follows the execution transport and configuration without inference.
func (a ProcessAdapter) CheckReadiness(ctx context.Context, input Input) error {
	input.ReadinessOnly = true
	input.SessionID = ""
	input.Prompt = ""
	_, err := a.Run(ctx, input, Hooks{})
	return err
}
