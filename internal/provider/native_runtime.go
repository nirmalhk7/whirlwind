package provider

import "whirlwind/internal/model"

func (s *providerSession) nativeRuntime(method string, params map[string]any) {
	if turn, ok := params["turnId"].(string); ok {
		s.mu.Lock()
		s.turnID = turn
		s.mu.Unlock()
	}
	switch method {
	case "turn/plan/updated":
		s.runtimeEvent("whirlwind/plan", params["plan"])
	case "item/started", "item/completed":
		item, _ := params["item"].(map[string]any)
		s.nativeTool(method, item)
	}
}

func (s *providerSession) nativeTool(method string, item map[string]any) {
	kind, _ := item["type"].(string)
	switch kind {
	case "commandExecution", "mcpToolCall", "dynamicToolCall", "fileChange":
	default:
		return
	}
	id, _ := item["id"].(string)
	if id == "" {
		return
	}
	s.mu.Lock()
	if s.tools == nil {
		s.tools = map[string]model.ToolActivity{}
	}
	tool := s.tools[id]
	tool.ID = id
	tool.Kind = kind
	tool.Status = "in_progress"
	if input := nativeToolInput(item); input != nil {
		tool.InputHash = runtimeHash(input)
	}
	if method == "item/completed" {
		tool.Status = nativeToolStatus(item)
		tool.OutputHash = runtimeHash(nativeToolOutput(item))
	}
	s.tools[id] = tool
	s.mu.Unlock()
	s.runtimeEvent("whirlwind/tool", tool)
}

func nativeToolInput(item map[string]any) any {
	for _, key := range []string{"command", "arguments", "changes"} {
		if item[key] != nil {
			return item[key]
		}
	}
	return nil
}
func nativeToolOutput(item map[string]any) any {
	for _, key := range []string{"aggregatedOutput", "error", "result", "contentItems"} {
		if item[key] != nil {
			return item[key]
		}
	}
	return item["status"]
}
func nativeToolStatus(item map[string]any) string {
	if exit, ok := item["exitCode"].(float64); ok && exit != 0 {
		return "failed"
	}
	if item["status"] == "failed" || item["status"] == "error" {
		return "failed"
	}
	return "completed"
}

func (s *providerSession) nativeContext(params map[string]any) {
	usage, _ := params["tokenUsage"].(map[string]any)
	last, _ := usage["last"].(map[string]any)
	s.contextUsage(map[string]any{"used": last["totalTokens"], "size": usage["modelContextWindow"]}, "codex")
}
