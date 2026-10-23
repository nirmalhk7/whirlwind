package model

type ThinkingChoice struct {
	ConfigID string `json:"config_id"`
	Value    string `json:"value"`
}

type AgentChoice struct {
	Provider string          `json:"provider"`
	Model    string          `json:"model"`
	Thinking *ThinkingChoice `json:"thinking,omitempty"`
}

type AgentConfig struct {
	InheritedFrom string        `json:"inherited_from,omitempty"`
	Role          string        `json:"role"`
	Chain         []AgentChoice `json:"chain"`
}

func AgentRoles() []string { return []string{"ideate", "plan", "code", "review", "submit"} }
