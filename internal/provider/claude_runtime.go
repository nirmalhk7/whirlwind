package provider

import "whirlwind/internal/model"

func (s *providerSession) claudeRuntime(params map[string]any) {
	if occupancy, ok := params["context"].(map[string]any); ok {
		s.contextUsage(occupancy, "claude")
	}
	message, _ := params["message"].(map[string]any)
	content, _ := message["content"].([]any)
	for _, part := range content {
		if block, ok := part.(map[string]any); ok {
			s.claudeTool(block)
		}
	}
}

func (s *providerSession) claudeTool(block map[string]any) {
	var tool model.ToolActivity
	switch block["type"] {
	case "tool_use":
		tool.ID, _ = block["id"].(string)
		tool.Kind, _ = block["name"].(string)
		tool.Status = "in_progress"
		tool.InputHash = runtimeHash(block["input"])
	case "tool_result":
		tool.ID, _ = block["tool_use_id"].(string)
		tool.Status = "completed"
		if block["is_error"] == true {
			tool.Status = "failed"
		}
		tool.OutputHash = runtimeHash(block["content"])
	default:
		return
	}
	s.mu.Lock()
	if s.tools == nil {
		s.tools = map[string]model.ToolActivity{}
	}
	if previous, ok := s.tools[tool.ID]; ok && tool.InputHash == "" {
		tool.Kind = previous.Kind
		tool.InputHash = previous.InputHash
	}
	s.tools[tool.ID] = tool
	s.mu.Unlock()
	s.runtimeEvent("whirlwind/tool", tool)
}
