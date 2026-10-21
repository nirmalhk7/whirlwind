package provider

import "encoding/json"

// Redact every protocol container carrying a harness memory call, including replayed lifecycle events.
func observedParams(message Message) any {
	var params any
	if json.Unmarshal(message.Params, &params) != nil {
		return nil
	}
	redactMemoryArguments(params)
	return params
}

func redactMemoryArguments(value any) {
	switch value := value.(type) {
	case map[string]any:
		if memoryInvocation(value) {
			for _, key := range []string{"arguments", "input", "rawInput"} {
				if _, present := value[key]; present {
					value[key] = map[string]string{"query": "[query omitted from protocol evidence]"}
				}
			}
		}
		for _, child := range value {
			redactMemoryArguments(child)
		}
	case []any:
		for _, child := range value {
			redactMemoryArguments(child)
		}
	}
}

func memoryInvocation(value map[string]any) bool {
	for _, key := range []string{"tool", "name", "title"} {
		switch value[key] {
		case "search_lessons", "mcp__whirlwind-memory__search_lessons":
			return true
		}
	}
	return false
}
