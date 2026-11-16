package model

import "encoding/json"

// PlanApproval is supervisor-owned metadata, never parsed from agent Markdown.
type PlanApproval struct {
	DocumentHash      string                   `json:"document_hash"`
	PlanVersion       int                      `json:"plan_version"`
	ConfigurationHash string                   `json:"configuration_hash"`
	References        []ConfigurationReference `json:"references"`
}

type ConfigurationReference struct {
	Kind    string          `json:"kind"`
	ID      string          `json:"id"`
	Version int             `json:"version"`
	Hash    string          `json:"hash"`
	Data    json.RawMessage `json:"data"`
}
