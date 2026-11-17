package model

import (
	"encoding/json"
)

type Event struct {
	ID         int64           `json:"id"`
	Kind       string          `json:"kind"`
	ResourceID string          `json:"resource_id"`
	Data       json.RawMessage `json:"data"`
	CreatedAt  string          `json:"created_at"`
}

type Record struct {
	CreatedOrder int64           `json:"created_order,omitempty"`
	ID           string          `json:"id"`
	Version      int             `json:"version"`
	Data         json.RawMessage `json:"data"`
	UpdatedAt    string          `json:"updated_at"`
}

type Notification struct {
	Role        string       `json:"role,omitempty"`
	Usage       *UsageReport `json:"usage,omitempty"`
	Occurrences int          `json:"occurrences,omitempty"`
	FirstAt     string       `json:"first_at,omitempty"`
	LastAt      string       `json:"last_at,omitempty"`
	Current     string       `json:"current,omitempty"`
	NextAction  string       `json:"next_action,omitempty"`
	Completed   []string     `json:"completed,omitempty"`
	EvidenceID  string       `json:"evidence_id,omitempty"`
	ID          string       `json:"id"`
	TaskID      string       `json:"plan_id,omitempty"`
	Kind        string       `json:"kind"`
	Message     string       `json:"message"`
	Resolved    bool         `json:"resolved"`
}
