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
	"time"

	"khan/internal/model"
)

type Input struct {
	Role      string
	CWD       string
	Prompt    string
	SessionID string
	Policy    model.Policy
	ReadOnly  bool
}
type Output struct {
	Text      string   `json:"text"`
	SessionID string   `json:"session_id"`
	Tokens    *int64   `json:"tokens,omitempty"`
	Cost      *float64 `json:"cost,omitempty"`
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
		if !p.Available || p.Kind == "unavailable" {
			continue
		}
		if p.Quota == "exhausted" {
			reset, e := time.Parse(time.RFC3339, p.ResetAt)
			if e != nil || time.Now().Before(reset) {
				continue
			}
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
	var mu sync.Mutex
	out := Output{}
	client := NewClient(in, h)
	defer client.Close()
	completed := make(chan string, 1)
	handler := func(c context.Context, m Message) (any, error) {
		if h.Event != nil {
			h.Event(m.Method, json.RawMessage(m.Params))
		}
		if strings.HasPrefix(m.Method, "fs/") || strings.HasPrefix(m.Method, "terminal/") {
			return client.Handle(c, m)
		}
		var p map[string]any
		_ = json.Unmarshal(m.Params, &p)
		switch m.Method {
		case "item/tool/requestUserInput":
			var request struct {
				Questions []struct {
					ID      string `json:"id"`
					Text    string `json:"question"`
					Secret  bool   `json:"isSecret"`
					Options []struct {
						Label string `json:"label"`
					} `json:"options"`
				} `json:"questions"`
			}
			if e := json.Unmarshal(m.Params, &request); e != nil {
				return nil, e
			}
			questions := []model.Question{}
			for _, q := range request.Questions {
				x := model.Question{ID: q.ID, Text: q.Text, Secret: q.Secret}
				for _, o := range q.Options {
					x.Options = append(x.Options, o.Label)
				}
				questions = append(questions, x)
			}
			return AnswerQuestions(c, h, questions, "codex")
		case "question/request":
			var request struct {
				Questions []model.Question `json:"questions"`
			}
			if e := json.Unmarshal(m.Params, &request); e != nil {
				return nil, e
			}
			return AnswerQuestions(c, h, request.Questions, "claude")
		case "session/update":
			u, _ := p["update"].(map[string]any)
			if u["sessionUpdate"] == "agent_message_chunk" {
				content, _ := u["content"].(map[string]any)
				txt, _ := content["text"].(string)
				mu.Lock()
				out.Text += txt
				mu.Unlock()
			}
		case "item/agentMessage/delta":
			txt, _ := p["delta"].(string)
			mu.Lock()
			out.Text += txt
			mu.Unlock()
		case "account/rateLimits/updated":
			if h.Event != nil {
				h.Event("khan/quota", json.RawMessage(m.Params))
			}
		case "thread/tokenUsage/updated":
			if usage, ok := p["tokenUsage"].(map[string]any); ok {
				if total, ok := usage["total"].(map[string]any); ok {
					if tokens, ok := total["totalTokens"].(float64); ok {
						value := int64(tokens)
						mu.Lock()
						out.Tokens = &value
						mu.Unlock()
					}
				}
			}
		case "turn/completed":
			turn, _ := p["turn"].(map[string]any)
			status, _ := turn["status"].(string)
			if status == "failed" && turn["error"] != nil {
				b, _ := json.Marshal(turn["error"])
				status += " " + string(b)
			}
			select {
			case completed <- status:
			default:
			}
		case "session/request_permission", "item/commandExecution/requestApproval", "item/fileChange/requestApproval", "permission/request":
			permission := Permission{Tool: m.Method, Raw: m.Params}
			if command, ok := p["command"].(string); ok && !strings.ContainsAny(command, ";&|><`$\\\"'()\n") {
				permission.Command = strings.Fields(command)
			}
			if input, ok := p["input"].(map[string]any); ok {
				if path, ok := input["file_path"].(string); ok {
					permission.Paths = append(permission.Paths, path)
				}
				if command, ok := input["command"].(string); ok && !strings.ContainsAny(command, ";&|><`$\\\"'()\n") {
					permission.Command = strings.Fields(command)
				}
			}
			if cmd, ok := p["command"].([]any); ok {
				for _, v := range cmd {
					if s, ok := v.(string); ok {
						permission.Command = append(permission.Command, s)
					}
				}
			}
			if paths, ok := p["paths"].([]any); ok {
				for _, v := range paths {
					if s, ok := v.(string); ok {
						permission.Paths = append(permission.Paths, s)
					}
				}
			}
			allowed := false
			var e error
			if h.Permission != nil {
				allowed, e = h.Permission(c, permission)
				if e != nil {
					return nil, e
				}
			}
			switch m.Method {
			case "session/request_permission":
				options, _ := p["options"].([]any)
				want := "reject_once"
				if allowed {
					want = "allow_once"
				}
				for _, v := range options {
					o, _ := v.(map[string]any)
					if o["kind"] == want {
						return map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": o["optionId"]}}, nil
					}
				}
				return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}, nil
			case "permission/request":
				return map[string]any{"allow": allowed}, nil
			default:
				decision := "decline"
				if allowed {
					decision = "accept"
				}
				return map[string]any{"decision": decision}, nil
			}
		case "cursor/ask_question", "cursor/create_plan":
			return nil, errors.New("user input required; send questions in structured plan output")
		}
		if len(m.ID) > 0 {
			return nil, errors.New("unsupported client method: " + m.Method)
		}
		return nil, nil
	}
	rpc, e := Start(ctx, a.Provider.Command, in.CWD, handler)
	if e != nil {
		return out, e
	}
	defer rpc.Close()
	switch a.Provider.Kind {
	case "acp":
		var init struct {
			Capabilities struct {
				Load bool `json:"loadSession"`
			} `json:"agentCapabilities"`
		}
		e = rpc.Call(ctx, "initialize", map[string]any{"protocolVersion": 1, "clientCapabilities": map[string]any{"fs": map[string]bool{"readTextFile": true, "writeTextFile": !in.ReadOnly}, "terminal": !in.ReadOnly}, "clientInfo": map[string]any{"name": "khan", "version": "0.1.0"}}, &init)
		if e != nil {
			return out, e
		}
		var session struct {
			ID    string `json:"sessionId"`
			Modes struct {
				Available []struct {
					ID string `json:"id"`
				} `json:"availableModes"`
			} `json:"modes"`
		}
		if in.SessionID != "" && init.Capabilities.Load {
			session.ID = in.SessionID
			e = rpc.Call(ctx, "session/load", map[string]any{"sessionId": session.ID, "cwd": in.CWD, "mcpServers": []any{}}, nil)
		} else {
			e = rpc.Call(ctx, "session/new", map[string]any{"cwd": in.CWD, "mcpServers": []any{}}, &session)
		}
		if e != nil {
			return out, e
		}
		out.SessionID = session.ID
		if h.Event != nil {
			h.Event("khan/session", map[string]string{"session_id": session.ID})
		}
		if in.ReadOnly {
			for _, mode := range session.Modes.Available {
				if mode.ID == "ask" {
					if e = rpc.Call(ctx, "session/set_mode", map[string]any{"sessionId": session.ID, "modeId": "ask"}, nil); e != nil {
						return out, e
					}
				}
			}
		}
		var result struct {
			Stop string `json:"stopReason"`
		}
		e = rpc.Call(ctx, "session/prompt", map[string]any{"sessionId": session.ID, "prompt": []any{map[string]any{"type": "text", "text": in.Prompt}}}, &result)
		if e == nil && result.Stop != "end_turn" {
			e = fmt.Errorf("ACP stopped: %s", result.Stop)
		}
	case "codex":
		e = rpc.Call(ctx, "initialize", map[string]any{"clientInfo": map[string]any{"name": "khan", "version": "0.1.0"}}, nil)
		if e != nil {
			return out, e
		}
		_ = rpc.Notify("initialized", map[string]any{})
		quotaCtx, quotaCancel := context.WithTimeout(ctx, 5*time.Second)
		var quota json.RawMessage
		if rpc.Call(quotaCtx, "account/rateLimits/read", map[string]any{}, &quota) == nil && h.Event != nil {
			h.Event("khan/quota", quota)
		}
		quotaCancel()
		params := map[string]any{"cwd": in.CWD, "approvalPolicy": "untrusted", "approvalsReviewer": "user", "sandbox": "workspace-write"}
		if in.ReadOnly {
			params["sandbox"] = "read-only"
		}
		method := "thread/start"
		if in.SessionID != "" {
			method = "thread/resume"
			params["threadId"] = in.SessionID
		}
		var thread struct {
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
		}
		e = rpc.Call(ctx, method, params, &thread)
		if e != nil {
			return out, e
		}
		out.SessionID = thread.Thread.ID
		if h.Event != nil {
			h.Event("khan/session", map[string]string{"session_id": out.SessionID})
		}
		e = rpc.Call(ctx, "turn/start", map[string]any{"threadId": out.SessionID, "input": []any{map[string]any{"type": "text", "text": in.Prompt}}}, nil)
		if e == nil {
			select {
			case status := <-completed:
				if status != "completed" {
					e = fmt.Errorf("Codex turn %s", status)
				}
			case <-ctx.Done():
				e = ctx.Err()
			case <-rpc.done:
				e = errors.New("Codex exited during turn")
			}
		}
	case "claude":
		var response Output
		e = rpc.Call(ctx, "run", map[string]any{"cwd": in.CWD, "prompt": in.Prompt, "session_id": in.SessionID, "read_only": in.ReadOnly}, &response)
		mu.Lock()
		out = response
		mu.Unlock()
	default:
		e = errors.New("unsupported provider kind")
	}
	mu.Lock()
	result := out
	mu.Unlock()
	return result, e
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
