package store

import (
	"encoding/json"
	"strings"
)

// Identifiers and outcomes survive payload truncation; never retain arbitrary text/tool content here.
func eventMetadata(content []byte) []byte {
	var value any
	if json.Unmarshal(content, &value) != nil {
		return []byte("{}")
	}
	result := map[string]any{}
	walkMetadata(value, "", 0, result)
	b, _ := json.Marshal(result)
	return b
}

var metadataFields = map[string]bool{"id": true, "request_id": true, "method": true, "session_id": true, "sessionId": true, "threadId": true, "thread_id": true, "turnId": true, "turn_id": true, "itemId": true, "item_id": true, "terminalId": true, "terminal_id": true, "exit_code": true, "exitCode": true, "signal": true, "status": true, "stopReason": true, "stop_reason": true, "resumed": true, "resolved_model": true, "previous_attempt_id": true, "type": true}

func walkMetadata(value any, path string, depth int, result map[string]any) {
	if depth > 4 || len(result) >= 32 {
		return
	}
	object, ok := value.(map[string]any)
	if !ok {
		return
	}
	for k, x := range object {
		key := k
		if path != "" {
			key = path + "." + k
		}
		if metadataFields[k] {
			retainMetadata(result, key, x)
		}
		walkMetadata(x, key, depth+1, result)
	}
}

func retainMetadata(result map[string]any, key string, value any) {
	switch scalar := value.(type) {
	case string:
		result[key] = string(Prefix([]byte(scalar), 256))
	case float64, bool:
		result[key] = scalar
	}
}

func streamKey(kind string, content []byte, replay bool) string {
	var object map[string]any
	if json.Unmarshal(content, &object) != nil {
		return ""
	}
	params := object
	if nested, ok := object["params"].(map[string]any); ok {
		params = nested
	}
	key := ""
	if kind == "whirlwind/stderr" {
		key = "stderr"
	}
	if kind == "item/agentMessage/delta" {
		key = "assistant"
	}
	if strings.HasSuffix(kind, "/outputDelta") {
		key = "tool-output"
	}
	if kind == "session/update" || kind == "whirlwind/replay" {
		if update, ok := params["update"].(map[string]any); ok && update["sessionUpdate"] == "agent_message_chunk" {
			key = "assistant"
		}
	}
	if key == "" {
		return ""
	}
	id, _ := params["itemId"].(string)
	if id == "" {
		id, _ = params["messageId"].(string)
	}
	if id == "" {
		id, _ = params["terminalId"].(string)
	}
	key += ":" + id
	if replay {
		key = "replay:" + key
	}
	return key
}
