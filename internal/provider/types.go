package provider

import (
	"context"
	"encoding/json"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
)

type Input struct {
	AgentHooks             model.AgentHookChecks `json:"agent_hooks,omitzero"`
	hookRuntimeDir         string
	PlannerOutputDirectory string
	PlannerPublicationPath string
	ReadinessOnly          bool
	NativeIntegrations     bool
	ProviderID             string
	ACPProfile             string
	DiscoverConnected      bool
	CatalogOnly            bool
	RequiredIntegrations   []model.IntegrationTarget
	IntegrationScopes      []model.IntegrationSource
	MCPBroker              *execution.MCPBroker
	MCPBrokerToken         string `json:"-"`
	Execution              *execution.Profile
	ExecutionProfile       string
	UnitID                 string
	OperationID            string
	Purpose                string
	Model                  string
	Thinking               *model.ThinkingChoice
	Role                   string
	CWD                    string
	Prompt                 string
	SessionID              string
	Policy                 model.Policy
	ReadOnly               bool
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
	FinalizeDelivery func(context.Context) (FinalizationResult, error)
	RunCheck         func(context.Context, string) (CheckResult, error)
	Questions        func(context.Context, []model.Question) (map[string]string, error)
	Event            func(string, any)
	Permission       func(context.Context, Permission) (bool, error)
}

type Adapter interface {
	// Run executes or resumes one agent prompt and exposes events, questions, and permissions through hooks.
	Run(context.Context, Input, Hooks) (Output, error)
}

type CheckResult struct {
	Phase            string `json:"phase,omitempty"`
	ValidationError  string `json:"validation_error,omitempty"`
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
