package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"whirlwind/internal/model"
)

func runtimeHash(value any) string {
	if value == nil {
		return ""
	}
	b, _ := json.Marshal(value)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (s *providerSession) acpRuntime(update map[string]any) {
	if s.replaying.Load() {
		return
	}
	switch update["sessionUpdate"] {
	case "usage_update":
		s.contextUsage(update, "acp")
		s.acpCost(update)
	case "tool_call", "tool_call_update":
		s.acpTool(update)
	case "plan":
		s.runtimeEvent("whirlwind/plan", update["entries"])
	}
}

func (s *providerSession) contextUsage(value map[string]any, source string) {
	used, ok := contextTokens(value["used"])
	size, valid := contextTokens(value["size"])
	if !ok || !valid {
		return
	}
	if size == 0 || used > size {
		return
	}
	usage := &model.ContextUsage{Used: used, Size: size, Source: source}
	s.mu.Lock()
	s.output.Context = usage
	s.mu.Unlock()
	s.runtimeEvent("whirlwind/context", usage)
}

func (s *providerSession) acpTool(update map[string]any) {
	id, _ := update["toolCallId"].(string)
	if id == "" {
		return
	}
	s.mu.Lock()
	if s.tools == nil {
		s.tools = map[string]model.ToolActivity{}
	}
	tool := s.tools[id]
	tool.ID = id
	if kind, ok := update["kind"].(string); ok {
		tool.Kind = kind
	}
	if status, ok := update["status"].(string); ok {
		tool.Status = status
	}
	if input := update["rawInput"]; input != nil {
		tool.InputHash = runtimeHash(input)
	}
	if output := update["rawOutput"]; output != nil {
		tool.OutputHash = runtimeHash(output)
	} else if content := update["content"]; content != nil {
		tool.OutputHash = runtimeHash(content)
	}
	tool.Paths = runtimePaths(update["locations"], tool.Paths)
	s.tools[id] = tool
	s.mu.Unlock()
	s.runtimeEvent("whirlwind/tool", tool)
}

func runtimePaths(value any, previous []string) []string {
	locations, ok := value.([]any)
	if !ok {
		return previous
	}
	paths := []string{}
	for _, item := range locations {
		if location, ok := item.(map[string]any); ok {
			if path, ok := location["path"].(string); ok {
				paths = append(paths, path)
			}
		}
	}
	return paths
}

func (s *providerSession) runtimeEvent(kind string, value any) {
	if !s.replaying.Load() && s.hooks.Event != nil {
		s.hooks.Event(kind, value)
		if kind == "whirlwind/tool" {
			s.stopIfRequested()
		}
	}
}
