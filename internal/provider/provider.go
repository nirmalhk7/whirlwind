package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"whirlwind/internal/model"
)

type Input struct {
	UnitID      string
	OperationID string
	Purpose     string
	Model       string
	Thinking    *model.ThinkingChoice
	Role        string
	CWD         string
	Prompt      string
	SessionID   string
	Policy      model.Policy
	ReadOnly    bool
}
type Output struct {
	AttemptID         string                `json:"attempt_id,omitempty"`
	Usage             *model.Usage          `json:"usage,omitempty"`
	Text              string                `json:"text"`
	ResolvedModel     string                `json:"resolved_model,omitempty"`
	EffectiveThinking *model.ThinkingChoice `json:"effective_thinking,omitempty"`
	SessionID         string                `json:"session_id"`
	Tokens            *int64                `json:"tokens,omitempty"`
	Cost              *float64              `json:"cost,omitempty"`
}
type Permission struct {
	Tool    string          `json:"tool"`
	Command []string        `json:"command,omitempty"`
	Paths   []string        `json:"paths,omitempty"`
	Raw     json.RawMessage `json:"raw"`
}
type Hooks struct {
	Questions  func(context.Context, []model.Question) (map[string]string, error)
	Event      func(string, any)
	Permission func(context.Context, Permission) (bool, error)
}
type Adapter interface {
	// Run executes or resumes one agent prompt and exposes events, questions, and permissions through hooks.
	Run(context.Context, Input, Hooks) (Output, error)
}
type ProcessAdapter struct{ Provider model.Provider }

// Defaults describes built-in provider commands and capabilities, including the explicitly unavailable Antigravity entry.
func Defaults(bridge string) []model.Provider {
	return []model.Provider{{ID: "codex", Name: "Codex", Kind: "codex", Command: []string{"codex", "app-server", "--listen", "stdio://"}, Capabilities: []string{"ideate", "plan", "code", "review", "resume", "permissions", "quota"}, Quota: "unknown"}, {ID: "cursor", Name: "Cursor", Kind: "acp", Command: []string{"agent", "acp"}, Capabilities: []string{"ideate", "plan", "code", "review", "resume", "permissions"}, Quota: "unknown"}, {ID: "claude", Name: "Claude", Kind: "claude", Command: []string{"node", bridge}, Capabilities: []string{"ideate", "plan", "code", "review", "resume", "permissions"}, Quota: "unknown"}, {ID: "antigravity", Name: "Antigravity", Kind: "unavailable", Capabilities: []string{}, Quota: "unknown", Limitation: "Consumer-plan third-party application wrapping unsupported; supported integration required."}}
}

// Available checks that a supported provider command executable exists; it does not prove authentication or quota.
func Available(p model.Provider) bool {
	if p.Kind == "unavailable" || len(p.Command) == 0 {
		return false
	}
	_, e := exec.LookPath(p.Command[0])
	return e == nil
}

// Route filters providers by availability, quota, and role, then ranks candidates by outcomes, configured preferences, and cost.
func Route(ps []model.Provider, role string, preferred []string, outcomes map[string]float64) (model.Provider, error) {
	c := []model.Provider{}
	for _, p := range ps {
		if !providerReady(p) {
			continue
		}
		if slices.Contains(p.Capabilities, role) {
			c = append(c, p)
		}
	}
	if len(c) == 0 {
		return model.Provider{}, errors.New("no available provider with required capability")
	}
	rank := func(p model.Provider) float64 {
		v := outcomes[p.ID] * 10
		for i, id := range preferred {
			if id == p.ID {
				v += float64(len(preferred) - i)
			}
		}
		if p.Cost != nil {
			v -= *p.Cost
		}
		return v
	}
	sort.SliceStable(c, func(i, j int) bool { return rank(c[i]) > rank(c[j]) })
	return c[0], nil
}

// Run drives a Codex, ACP, or Claude-bridge session over stdio, handling streamed output, tool permissions, user questions, and cancellation.
func (a ProcessAdapter) Run(ctx context.Context, in Input, h Hooks) (Output, error) {
	p := ExecutionProvider(a.Provider, in.Thinking)
	s := &providerSession{ctx: ctx, input: in, hooks: h, client: NewClient(in, h), completed: make(chan string, 1)}
	defer s.client.Close()
	rpc, err := Start(ctx, p.Command, in.CWD, s.handle, h.Event)
	if err != nil {
		return s.output, err
	}
	s.rpc = rpc
	defer rpc.Close()
	switch p.Kind {
	case "acp":
		err = s.runACP()
	case "codex":
		err = s.runCodex()
	case "claude":
		err = s.runClaude()
	default:
		err = errors.New("unsupported provider kind")
	}
	s.mu.Lock()
	result := s.output
	s.mu.Unlock()
	return result, err
}

type providerSession struct {
	ctx       context.Context
	input     Input
	hooks     Hooks
	client    *Client
	rpc       *RPC
	mu        sync.Mutex
	output    Output
	completed chan string
	replaying atomic.Bool
}

// JSON extracts the outer JSON object or array from agent text and unmarshals it into the supplied destination.
func JSON(text string, v any) error {
	text = strings.TrimSpace(text)
	if json.Unmarshal([]byte(text), v) == nil {
		return nil
	}
	start := strings.IndexAny(text, "{[")
	if start < 0 {
		return errors.New("agent must return JSON")
	}
	dec := json.NewDecoder(strings.NewReader(text[start:]))
	return dec.Decode(v)
}

func providerReady(p model.Provider) bool {
	if !p.Available || p.Kind == "unavailable" {
		return false
	}
	if p.Quota == "exhausted" {
		reset, e := time.Parse(time.RFC3339, p.ResetAt)
		if e != nil || time.Now().Before(reset) {
			return false
		}
	}
	return true
}

func (s *providerSession) handle(c context.Context, m Message) (any, error) {
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
	switch m.Method {
	case "item/tool/requestUserInput", "question/request",
		"session/request_permission", "item/commandExecution/requestApproval",
		"item/fileChange/requestApproval", "permission/request",
		"cursor/ask_question", "cursor/create_plan":
		return s.handleRequest(c, m, p)
	case "agent/event":
		s.claudeEvent(p)
	case "session/update":
		s.sessionUpdate(p)
	case "item/agentMessage/delta":
		s.messageDelta(p)
	case "account/rateLimits/updated":
		s.quotaUpdate(m)
	case "thread/tokenUsage/updated":
		s.tokenUsage(p)
	case "turn/completed":
		s.turnCompleted(p)
	}
	if len(m.ID) > 0 {
		return nil, errors.New("unsupported client method: " + m.Method)
	}
	return nil, nil
}

func (s *providerSession) handleRequest(ctx context.Context, m Message, params map[string]any) (any, error) {
	switch m.Method {
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

func (s *providerSession) runCodex() error {
	var e error
	e = s.rpc.Call(s.ctx, "initialize", map[string]any{"clientInfo": map[string]any{"name": "whirlwind", "version": "0.1.0"}}, nil)
	if e != nil {
		return e
	}
	_ = s.rpc.Notify("initialized", map[string]any{})
	quotaCtx, quotaCancel := context.WithTimeout(s.ctx, 5*time.Second)
	var quota json.RawMessage
	if s.rpc.Call(quotaCtx, "account/rateLimits/read", map[string]any{}, &quota) == nil && s.hooks.Event != nil {
		s.hooks.Event("whirlwind/quota", quota)
	}
	quotaCancel()
	params := map[string]any{"cwd": s.input.CWD, "approvalPolicy": "untrusted", "approvalsReviewer": "user", "sandbox": "workspace-write"}
	if s.input.Model != "" {
		params["model"] = s.input.Model
	}
	if s.input.ReadOnly {
		params["sandbox"] = "read-only"
	}
	method := "thread/start"
	if s.input.SessionID != "" {
		method = "thread/resume"
		params["threadId"] = s.input.SessionID
	}
	var thread struct {
		Model  string `json:"model"`
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	e = s.rpc.Call(s.ctx, method, params, &thread)
	if e != nil {
		return e
	}
	if thread.Thread.ID == "" {
		return errors.New("codex returned empty thread ID")
	}
	s.mu.Lock()
	s.output.SessionID = thread.Thread.ID
	s.output.ResolvedModel = thread.Model
	s.mu.Unlock()
	if s.hooks.Event != nil {
		s.hooks.Event("whirlwind/session", map[string]any{"session_id": s.output.SessionID, "resumed": s.input.SessionID != ""})
	}
	e = s.rpc.Call(s.ctx, "turn/start", map[string]any{"threadId": s.output.SessionID, "input": []any{map[string]any{"type": "text", "text": s.input.Prompt}}}, nil)
	if e != nil {
		return e
	}
	return s.waitCodexTurn()
}

func (s *providerSession) runClaude() error {
	var e error
	var response Output
	e = s.rpc.Call(s.ctx, "run", map[string]any{"cwd": s.input.CWD, "prompt": s.input.Prompt, "session_id": s.input.SessionID, "read_only": s.input.ReadOnly, "model": s.input.Model}, &response)
	s.mu.Lock()
	if e == nil {
		s.output = response
	} else {
		if response.SessionID != "" {
			s.output.SessionID = response.SessionID
		}
	}
	s.mu.Unlock()

	return e
}

func (s *providerSession) waitCodexTurn() error {
	var e error
	select {
	case status := <-s.completed:
		if status != "completed" {
			e = fmt.Errorf("%s turn %s", "Codex", status)
		}
	case <-s.ctx.Done():
		e = s.ctx.Err()
	case <-s.rpc.done:
		e = fmt.Errorf("%s exited during turn", "Codex")
	}
	return e
}
