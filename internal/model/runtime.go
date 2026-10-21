package model

type ContextUsage struct {
	Used   int64  `json:"used"`
	Size   int64  `json:"size"`
	Source string `json:"source"`
}

type ToolActivity struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	Status     string   `json:"status"`
	InputHash  string   `json:"input_hash"`
	OutputHash string   `json:"output_hash"`
	Paths      []string `json:"paths,omitempty"`
}

type RuntimeSnapshot struct {
	Uncertain      bool          `json:"uncertain,omitempty"`
	Warnings       []string      `json:"warnings,omitempty"`
	Context        *ContextUsage `json:"context,omitempty"`
	RecoveryReason string        `json:"recovery_reason,omitempty"`
	CheckpointID   string        `json:"checkpoint_id,omitempty"`
	Recoveries     int           `json:"recoveries"`
	Rotations      int           `json:"rotations"`
	Generation     int           `json:"generation"`
}
