package provider

import (
	"encoding/json"
	"strings"

	"whirlwind/internal/model"
)

// nativeCall is host-attributed protocol metadata, never assistant-authored JSON.
type nativeCall struct {
	Ref, Tool string
	ReadOnly  bool
	Complete  bool
}

type nativeIdentity struct{ server, tool, connector string }
type nativeEvidenceEvent struct {
	item     map[string]any
	kind, id string
}

func nativeString(p map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := p[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func nativeToolName(server, tool string) string {
	name := strings.Map(func(r rune) rune {
		if strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-", r) {
			return r
		}
		return '_'
	}, server)
	return "mcp__" + name + "__" + tool
}

func nativeCallIdentity(p map[string]any) nativeIdentity {
	identity := nativeIdentity{server: nativeString(p, "server", "serverName"), tool: nativeString(p, "tool", "toolName"), connector: nativeString(p, "connectorId")}
	name := nativeString(p, "name")
	if app, ok := p["appContext"].(map[string]any); ok {
		identity.connector = nativeString(app, "connectorId")
	}
	if identity.server == "" && identity.tool == "" && name == "" {
		identity.readRawInput(p)
	}
	if identity.tool == "" {
		identity.tool = name
	}
	return identity
}

func (identity *nativeIdentity) readRawInput(p map[string]any) {
	if raw, ok := p["rawInput"].(map[string]any); ok {
		identity.server = nativeString(raw, "server", "serverName")
		identity.tool = nativeString(raw, "tool", "toolName")
	}
}

func (identity nativeIdentity) accepts(item model.Integration) bool {
	if !item.Enabled || !item.Callable {
		return false
	}
	if identity.connector != "" {
		return item.Ref == "app://"+identity.connector
	}
	return identity.server == "" || item.Ref == "mcp://"+identity.server
}

func nativeDescriptorMatches(item model.Integration, descriptor model.IntegrationTool, name string) bool {
	if name == descriptor.Name {
		return true
	}
	return item.Kind == "mcp" && name == nativeToolName(strings.TrimPrefix(item.Ref, "mcp://"), descriptor.Name)
}

func (identity nativeIdentity) matches(item model.Integration) []nativeCall {
	matches := []nativeCall{}
	for _, descriptor := range item.Tools {
		if nativeDescriptorMatches(item, descriptor, identity.tool) {
			matches = append(matches, nativeCall{Ref: item.Ref, Tool: descriptor.Name, ReadOnly: descriptor.ReadOnly})
		}
	}
	return matches
}

// matchNativeCall requires a unique catalog match. Unqualified ACP names can be
// useful metadata, but never authorize a same-named tool from multiple servers.
func (s *providerSession) matchNativeCall(p map[string]any) (nativeCall, bool) {
	identity := nativeCallIdentity(p)
	if identity.tool == "" {
		return nativeCall{}, false
	}
	matches := []nativeCall{}
	for _, item := range s.output.Integrations {
		if identity.accepts(item) {
			matches = append(matches, identity.matches(item)...)
		}
	}
	if len(matches) == 0 {
		return nativeCall{}, false
	}
	return matches[len(matches)-1], len(matches) == 1
}

func (s *providerSession) nativeReadPermission(params map[string]any) bool {
	if !s.input.NativeIntegrations {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if call, ok := params["toolCall"].(map[string]any); ok {
		return s.nativeReadCallPermission(call, "toolCallId", "id")
	}
	return s.nativeReadCallPermission(params, "itemId", "toolCallId")
}

func (s *providerSession) nativeReadCallPermission(params map[string]any, idKeys ...string) bool {
	id := nativeString(params, idKeys...)
	if pending, found := s.nativeCalls[id]; id != "" && found {
		return pending.Ref != "" && pending.ReadOnly && !pending.Complete
	}
	matched, found := s.matchNativeCall(params)
	return found && matched.ReadOnly
}

func nativeResultFailed(item map[string]any) bool {
	return item["error"] != nil || item["isError"] == true || item["is_error"] == true
}

func nativeToolFailed(item map[string]any) bool {
	if nativeResultFailed(item) {
		return true
	}
	for _, key := range []string{"rawOutput", "result"} {
		output, _ := item[key].(map[string]any)
		if nativeResultFailed(output) {
			return true
		}
	}
	return false
}

func nativeToolEvent(m Message) (nativeEvidenceEvent, bool) {
	switch m.Method {
	case "item/started", "item/completed", "session/update":
	default:
		return nativeEvidenceEvent{}, false
	}
	var params map[string]any
	if json.Unmarshal(m.Params, &params) != nil {
		return nativeEvidenceEvent{}, false
	}
	item, _ := params["item"].(map[string]any)
	if m.Method == "session/update" {
		item, _ = params["update"].(map[string]any)
	}
	kind := nativeString(item, "type", "sessionUpdate")
	if !nativeEvidenceKind(kind) {
		return nativeEvidenceEvent{}, false
	}
	id := nativeString(item, "id", "toolCallId")
	return nativeEvidenceEvent{item: item, kind: kind, id: id}, id != ""
}

func nativeEvidenceKind(kind string) bool {
	switch kind {
	case "mcpToolCall", "tool_call", "tool_call_update":
		return true
	default:
		return false
	}
}

func nativeTerminalStatus(status string) bool {
	switch status {
	case "completed", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func (s *providerSession) trackNativeCall(event nativeEvidenceEvent) (nativeCall, bool) {
	call, exists := s.nativeCalls[event.id]
	if exists && call.Complete {
		return nativeCall{}, false
	}
	if event.kind == "tool_call_update" {
		return call, exists
	}
	call, matched := s.matchNativeCall(event.item)
	if !matched {
		call = nativeCall{}
	}
	s.nativeCalls[event.id] = call
	return call, true
}

func (s *providerSession) captureIntegrationTool(m Message) {
	if !s.input.NativeIntegrations || s.replaying.Load() {
		return
	}
	event, valid := nativeToolEvent(m)
	if !valid {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nativeCalls == nil {
		s.nativeCalls = map[string]nativeCall{}
	}
	call, tracked := s.trackNativeCall(event)
	if !tracked {
		return
	}
	status := nativeString(event.item, "status")
	if !nativeTerminalStatus(status) {
		return
	}
	call.Complete = true
	s.nativeCalls[event.id] = call
	if call.Ref != "" {
		s.output.ToolCalls = append(s.output.ToolCalls, ToolEvidence{Ref: call.Ref, Tool: call.Tool, CallID: event.id, Success: status == "completed" && !nativeToolFailed(event.item)})
	}
}
