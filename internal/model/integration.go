package model

type IntegrationTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	ReadOnly    bool   `json:"read_only"`
}
type Integration struct {
	Provider string            `json:"provider"`
	Ref      string            `json:"ref"`
	Name     string            `json:"name"`
	Kind     string            `json:"kind"`
	Enabled  bool              `json:"enabled"`
	Callable bool              `json:"callable"`
	Error    string            `json:"error,omitempty"`
	Tools    []IntegrationTool `json:"tools,omitempty"`
}
