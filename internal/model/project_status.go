package model

// ProjectStatus keeps invalid configured paths visible without scheduling them.
type ProjectStatus struct {
	Path      string `json:"path"`
	ProjectID string `json:"project_id,omitempty"`
	Status    string `json:"status"`
	Detail    string `json:"detail,omitempty"`
}
