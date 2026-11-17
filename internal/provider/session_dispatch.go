package provider

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
)

type providerSession struct {
	checkServers   []any
	nativeServers  []any
	nativeCalls    map[string]nativeCall
	nativePolicies map[string]string
	ctx            context.Context
	input          Input
	hooks          Hooks
	client         *Client
	rpc            *RPC
	mu             sync.Mutex
	output         Output
	replaying      atomic.Bool
	messageID      string
}

func (s *providerSession) handle(c context.Context, m Message) (any, error) {
	s.captureIntegrationTool(m)
	if s.hooks.Event != nil {
		kind := m.Method
		if s.replaying.Load() {
			kind = "whirlwind/replay"
		}
		s.hooks.Event(kind, map[string]any{"method": m.Method, "request_id": m.ID, "params": json.RawMessage(m.Params)})
	}
	if strings.HasPrefix(m.Method, "fs/") || strings.HasPrefix(m.Method, "terminal/") {
		return s.client.Handle(c, m)
	}
	var p map[string]any
	_ = json.Unmarshal(m.Params, &p)
	if result, handled, err := s.handleToolRequest(c, m, p); handled {
		return result, err
	}
	s.handleNotification(m, p)
	if len(m.ID) > 0 {
		return nil, errors.New("unsupported client method: " + m.Method)
	}
	return nil, nil
}

func (s *providerSession) handleToolRequest(ctx context.Context, m Message, params map[string]any) (any, bool, error) {
	switch m.Method {
	case "whirlwind/run_check":
		result, err := s.runCheckTool(ctx, m.Params)
		return result, true, err
	case "item/tool/call":
		result, err := s.dynamicCheck(ctx, m.Params)
		return result, true, err
	case "mcpServer/elicitation/request", "item/permissions/requestApproval", "item/tool/requestUserInput", "question/request",
		"session/request_permission", "item/commandExecution/requestApproval",
		"item/fileChange/requestApproval", "permission/request",
		"cursor/ask_question", "cursor/create_plan":
		result, err := s.handleRequest(ctx, m, params)
		return result, true, err
	default:
		return nil, false, nil
	}
}

func (s *providerSession) handleNotification(m Message, params map[string]any) {
	switch m.Method {
	case "agent/event":
		s.claudeEvent(params)
	case "session/update":
		s.sessionUpdate(params)
	case "item/agentMessage/delta":
		s.messageDelta(params)
	case "account/rateLimits/updated":
		s.quotaUpdate(m)
	case "thread/tokenUsage/updated":
		s.tokenUsage(params)
	}
}

func (s *providerSession) handleRequest(ctx context.Context, m Message, params map[string]any) (any, error) {
	switch m.Method {
	case "mcpServer/elicitation/request":
		return s.mcpElicitation(ctx, m, params)
	case "item/tool/requestUserInput":
		return s.codexQuestions(ctx, m)
	case "question/request":
		return s.claudeQuestions(ctx, m)
	case "cursor/ask_question", "cursor/create_plan":
		return nil, errors.New("user input required; send questions in structured plan output")
	default:
		return s.requestPermission(ctx, m, params)
	}
}
