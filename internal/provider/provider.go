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

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
)

type Input struct {
	ReadinessOnly        bool
	NativeIntegrations   bool
	ProviderID           string
	ACPProfile           string
	DiscoverConnected    bool
	CatalogOnly          bool
	RequiredIntegrations []model.IntegrationTarget
	IntegrationScopes    []model.IntegrationSource
	MCPBroker            *execution.MCPBroker
	MCPBrokerToken       string `json:"-"`
	Execution            *execution.Profile
	ExecutionProfile     string
	UnitID               string
	OperationID          string
	Purpose              string
	Model                string
	Thinking             *model.ThinkingChoice
	Role                 string
	CWD                  string
	Prompt               string
	SessionID            string
	Policy               model.Policy
	ReadOnly             bool
}
type ToolEvidence struct {
	Ref     string `json:"ref"`
	Tool    string `json:"tool"`
	CallID  string `json:"call_id"`
	Success bool   `json:"success"`
}
type Output struct {
	ToolCalls         []ToolEvidence        `json:"tool_calls,omitempty"`
	Integrations      []model.Integration   `json:"integrations,omitempty"`
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
	RunCheck   func(context.Context, string) (CheckResult, error)
	Questions  func(context.Context, []model.Question) (map[string]string, error)
	Event      func(string, any)
	Permission func(context.Context, Permission) (bool, error)
}
type Adapter interface {
	// Run executes or resumes one agent prompt and exposes events, questions, and permissions through hooks.
	Run(context.Context, Input, Hooks) (Output, error)
}
type CheckResult struct {
	Phase            string `json:"phase,omitempty"`
	BaselineAccepted bool   `json:"baseline_accepted,omitempty"`
	ExecutionProfile string `json:"execution_profile"`
	CheckID          string `json:"check_id"`
	Head             string `json:"head"`
	Snapshot         string `json:"snapshot"`
	Passed           bool   `json:"passed"`
	ExitCode         *int   `json:"exit_code"`
	Output           string `json:"output"`
	OutputArtifact   string `json:"output_artifact,omitempty"`
	Truncated        bool   `json:"truncated"`
	DurationMS       int64  `json:"duration_ms"`
}
type ProcessAdapter struct{ Provider model.Provider }

// Defaults describes built-in provider commands and capabilities, including the explicitly unavailable Antigravity entry.
func Defaults(bridge string) []model.Provider {
	_ = bridge // Retained for callers from older configurations; Claude now uses its bundled ACP profile.
	return []model.Provider{{ID: "codex", Name: "Codex", Kind: "acp", ACPProfile: "codex", Command: ACPCommand(model.Provider{Kind: "acp", ACPProfile: "codex"}), Capabilities: []string{"ideate", "plan", "code", "review", "finalizer", "submit", "resume", "permissions", "quota"}, Quota: "unknown"}, {ID: "cursor", Name: "Cursor", Kind: "acp", ACPProfile: "cursor", Command: ACPCommand(model.Provider{Kind: "acp", ACPProfile: "cursor"}), Capabilities: []string{"ideate", "plan", "code", "review", "finalizer", "submit", "resume", "permissions"}, Quota: "unknown"}, {ID: "claude", Name: "Claude", Kind: "acp", ACPProfile: "claude", Command: ACPCommand(model.Provider{Kind: "acp", ACPProfile: "claude"}), Capabilities: []string{"ideate", "plan", "code", "review", "finalizer", "submit", "resume", "permissions"}, Quota: "unknown"}, {ID: "antigravity", Name: "Antigravity", Kind: "unavailable", Capabilities: []string{}, Quota: "unknown", Limitation: "Consumer-plan third-party application wrapping unsupported; supported integration required."}}
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
func (a ProcessAdapter) Run(ctx context.Context, in Input, h Hooks) (result Output, err error) {
	p, err := ResolveExecutionProvider(a.Provider)
	if err != nil {
		return Output{SessionID: in.SessionID}, err
	}
	if in.NativeIntegrations {
		p.NativeIntegrations = true
	}
	in.ProviderID = a.Provider.ID
	in.ACPProfile = p.ACPProfile
	s := &providerSession{ctx: ctx, input: in, hooks: h, client: NewClient(in, h), output: Output{SessionID: in.SessionID}}
	defer func() { err = errors.Join(err, s.client.Close()) }()
	profile, err := s.sessionExecutionProfile(ctx, p, in)
	if err != nil {
		return s.output, err
	}
	standalone := profile.StandaloneRuntime
	transferred := false
	defer func() {
		if standalone && !transferred {
			err = errors.Join(err, completeStandaloneProfile(profile))
		}
	}()
	cleanup, err := s.prepareProviderIntegrations(p, a.Provider, &profile)
	if err != nil {
		return s.output, err
	}
	if cleanup != nil {
		defer cleanup()
	}
	rpc, err := StartWithProfile(ctx, p.Command, profile, s.handle, h.Event)
	if err != nil {
		return s.output, actionableACPError(p, err)
	}
	transferred = true
	s.rpc = rpc
	defer func() { err = errors.Join(err, rpc.Close()) }()
	err = authenticationACPError(p, s.runProviderKind(p.Kind))
	s.mu.Lock()
	result = s.output
	s.mu.Unlock()
	return result, err
}

func (s *providerSession) sessionExecutionProfile(ctx context.Context, executionProvider model.Provider, input Input) (execution.Profile, error) {
	profile, err := s.client.executionProfile()
	if input.Execution != nil {
		profile = *input.Execution
	}
	if err != nil {
		return profile, err
	}
	if input.Execution == nil {
		profile, err = execution.ForProvider(input.CWD, input.ReadOnly, "", input.Policy.Sandbox, executionProvider.Command)
		if err != nil {
			return profile, err
		}
		profile.AllowedPaths = append([]string{}, input.Policy.AllowedPaths...)
		profile, err = pinStandaloneProfile(ctx, profile, "")
		if err != nil {
			return execution.Profile{}, err
		}
	}
	profile.MCPBroker = input.MCPBroker
	profile.MCPBrokerToken = input.MCPBrokerToken
	return profile, nil
}

func (s *providerSession) prepareProviderIntegrations(p, provider model.Provider, profile *execution.Profile) (func(), error) {
	if s.input.MCPBroker != nil {
		if s.input.NativeIntegrations {
			return nil, errors.New("selected read-only MCP broker conflicts with raw native integration mounts")
		}
		if s.hasCheckTool() {
			return nil, errors.New("selected integration broker conflicts with reviewer check broker; use separate agent attempts")
		}
		if p.Kind != "acp" {
			return nil, errors.New("selected integration broker requires the configured ACP transport")
		}
		if err := s.prepareBrokerMCP(profile); err != nil {
			return nil, err
		}
	}
	if p.Kind == "acp" && p.ACPProfile != "" {
		if err := provisionACPAuth(profile, provider); err != nil {
			return nil, err
		}
	}
	if s.input.NativeIntegrations {
		if err := ProvisionNativeIntegrations(profile, provider); err != nil {
			return nil, err
		}
		if p.Kind == "acp" {
			if err := s.prepareCursorIntegrations(p, *profile); err != nil {
				return nil, err
			}
		}
	}
	s.input.Execution = profile
	s.input.CWD = profile.CWD
	s.client.input.CWD = profile.CWD
	if p.Kind != "acp" && p.Kind != "claude" {
		return nil, nil
	}
	return s.prepareCheckMCP(s.ctx, profile)
}

func (s *providerSession) runProviderKind(kind string) error {
	switch kind {
	case "acp":
		return s.runACP()
	case "codex":
		return fmt.Errorf("native Codex execution is unsupported; configure its ACP agent and set kind: acp")
	case "claude":
		return fmt.Errorf("Claude bridge execution is unsupported; configure its ACP agent and set kind: acp")
	default:
		return errors.New("unsupported provider kind")
	}
}

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
