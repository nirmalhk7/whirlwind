package model

type Check struct {
	Mode             string            `json:"mode,omitempty" yaml:"mode"`
	DependsOn        []string          `json:"depends_on,omitempty" yaml:"depends_on"`
	Required         *bool             `json:"required,omitempty" yaml:"required"`
	ReportFormat     string            `json:"report_format,omitempty" yaml:"report_format"`
	Environment      map[string]string `json:"environment,omitempty" yaml:"environment"`
	OutputPaths      []string          `json:"output_paths,omitempty" yaml:"output_paths"`
	Reusable         bool              `json:"reusable,omitempty" yaml:"reusable"`
	FreshnessSeconds int               `json:"freshness_seconds,omitempty" yaml:"freshness_seconds"`
	Name             string            `json:"name" yaml:"name"`
	Command          []string          `json:"command" yaml:"command"`
	TimeoutSeconds   int               `json:"timeout_seconds" yaml:"timeout_seconds"`
}
type RiskRule struct {
	APISymbols []string `json:"api_symbols,omitempty" yaml:"api_symbols"`
	CodeTypes  []string `json:"code_types,omitempty" yaml:"code_types"`
	Level      string   `json:"level" yaml:"level"`
	Paths      []string `json:"paths" yaml:"paths"`
	Checks     []Check  `json:"checks" yaml:"checks"`
}
type Embeddings struct {
	Endpoint string `json:"endpoint" yaml:"endpoint"`
	Model    string `json:"model" yaml:"model"`
	KeyEnv   string `json:"key_env" yaml:"key_env"`
	Enabled  bool   `json:"enabled" yaml:"enabled"`
}
type Policy struct {
	Quality            *QualityPolicy `json:"quality,omitempty" yaml:"quality"`
	AdaptiveRouting    bool           `json:"adaptive_routing,omitempty" yaml:"adaptive_routing"`
	Sandbox            *SandboxPolicy `json:"sandbox,omitempty" yaml:"sandbox"`
	ContextInputTokens int            `json:"context_input_tokens,omitempty" yaml:"context_input_tokens"`
	ParallelUnits      bool           `json:"parallel_units,omitempty" yaml:"parallel_units"`
	Budget             *BudgetPolicy  `json:"budget,omitempty" yaml:"budget"`
	RunTimeoutSeconds  int            `json:"run_timeout_seconds,omitempty" yaml:"run_timeout_seconds"`
	Checks             []Check        `json:"checks" yaml:"checks"`
	RiskRules          []RiskRule     `json:"risk_rules" yaml:"risk_rules"`
	AllowedPaths       []string       `json:"allowed_paths" yaml:"allowed_paths"`
	Commands           [][]string     `json:"commands" yaml:"commands"`
	Providers          []string       `json:"providers" yaml:"providers"`
	GlobalSlots        int            `json:"global_slots" yaml:"global_slots"`
	ProjectSlots       int            `json:"project_slots" yaml:"project_slots"`
	Embeddings         *Embeddings    `json:"embeddings,omitempty" yaml:"embeddings"`
}
