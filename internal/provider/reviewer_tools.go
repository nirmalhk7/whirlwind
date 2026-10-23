package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
)

func checkSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"check_id": map[string]any{"type": "string", "description": "Exact committed project check ID"}}, "required": []string{"check_id"}, "additionalProperties": false}
}

func (s *providerSession) hasCheckTool() bool {
	return (s.input.ReadOnly && s.input.Role == "review" || s.input.Role == "code") && s.hooks.RunCheck != nil
}

func (s *providerSession) checkTools() []any {
	if !s.hasCheckTool() {
		return nil
	}
	return []any{map[string]any{"type": "function", "name": "run_check", "description": "Run one committed check ID in an isolated snapshot; accepts no command, path, or shell text.", "inputSchema": checkSchema()}}
}

func (s *providerSession) runCheckTool(ctx context.Context, raw json.RawMessage) (CheckResult, error) {
	if !s.hasCheckTool() {
		return CheckResult{}, errors.New("reviewer check broker unavailable")
	}
	var args struct {
		CheckID string `json:"check_id"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return CheckResult{}, err
	}
	if args.CheckID == "" {
		return CheckResult{}, errors.New("committed check ID required")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return CheckResult{}, errors.New("single check request required")
	}
	return s.hooks.RunCheck(ctx, args.CheckID)
}

func (s *providerSession) dynamicCheck(ctx context.Context, raw json.RawMessage) (any, error) {
	var request struct {
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	if request.Tool != "run_check" {
		return nil, errors.New("unknown dynamic tool")
	}
	result, err := s.runCheckTool(ctx, request.Arguments)
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(result)
	return map[string]any{"success": true, "contentItems": []any{map[string]string{"type": "inputText", "text": string(b)}}}, nil
}
