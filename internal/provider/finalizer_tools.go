package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
)

func (s *providerSession) hasFinalizationTool() bool {
	return s.input.Role == "finalizer" && s.input.Purpose == "local_finalization" && s.input.ReadOnly && s.hooks.FinalizeDelivery != nil
}
func (s *providerSession) hasScopedTool() bool { return s.hasCheckTool() || s.hasFinalizationTool() }
func finalizationTool() map[string]any {
	return map[string]any{"type": "function", "name": "finalize_delivery", "description": "Finalize this attempt's exact accepted code version locally. Accepts no identities, paths, commands or Git arguments.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}}
}
func (s *providerSession) callScopedTool(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	switch name {
	case "run_check":
		return s.runCheckTool(ctx, raw)
	case "finalize_delivery":
		if !s.hasFinalizationTool() {
			return nil, errors.New("scoped finalization operation unavailable for this role")
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		var args map[string]any
		if err := decoder.Decode(&args); err != nil {
			return nil, err
		}
		if args == nil || len(args) != 0 {
			return nil, errors.New("finalization operation requires an empty object")
		}
		if decoder.Decode(new(any)) != io.EOF {
			return nil, errors.New("single finalization request required")
		}
		return s.hooks.FinalizeDelivery(ctx)
	default:
		return nil, errors.New("unknown scoped tool")
	}
}
