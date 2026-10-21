package provider

import (
	"context"
	"encoding/json"
	"errors"
)

func (s *providerSession) memoryServers() []any {
	if s.input.Memory == nil {
		return []any{}
	}
	m := s.input.Memory
	return []any{map[string]any{"name": "whirlwind-memory", "command": m.Command, "args": []string{"memory-mcp"}, "env": []any{map[string]string{"name": "WHIRLWIND_MEMORY_URL", "value": m.URL}, map[string]string{"name": "WHIRLWIND_MEMORY_TOKEN", "value": m.Token}}}}
}

func memoryToolDefinition() []any {
	return []any{map[string]any{"name": "search_lessons", "description": "Search current project and role lessons when an unexpected problem appears. At most two compact searches per operation.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string", "minLength": 1, "maxLength": 512}}, "required": []string{"query"}, "additionalProperties": false}}}
}

func (s *providerSession) memoryToolCall(ctx context.Context, params map[string]any) (any, error) {
	if params["tool"] != "search_lessons" || s.hooks.SearchLessons == nil {
		return nil, errors.New("unsupported harness tool")
	}
	raw, _ := json.Marshal(params["arguments"])
	var input struct {
		Query string `json:"query"`
	}
	err := json.Unmarshal(raw, &input)
	text := "Invalid memory query"
	if err == nil {
		text, err = s.hooks.SearchLessons(ctx, input.Query)
	}
	if err != nil {
		text = err.Error()
	}
	return map[string]any{"success": err == nil, "contentItems": []any{map[string]string{"type": "inputText", "text": text}}}, nil
}
