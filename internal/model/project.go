package model

import (
	"slices"
)

type Project struct {
	Status          string           `json:"status,omitempty"`
	RemoteURL       string           `json:"remote_url,omitempty"`
	Paused          bool             `json:"paused"`
	DefaultBranch   string           `json:"default_branch"`
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	Repo            string           `json:"repo"`
	Path            string           `json:"path"`
	Policy          Policy           `json:"policy"`
	PolicyHash      string           `json:"policy_hash"`
	QualityIdentity *QualityIdentity `json:"quality_identity,omitempty"`
	WebhookURL      string           `json:"webhook_url,omitempty"`
}

type Issue struct {
	Number    int      `json:"number"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	UpdatedAt string   `json:"updated_at"`
	State     string   `json:"state"`
	Labels    []string `json:"labels"`
	URL       string   `json:"url"`
}

// Eligible requires an open issue carrying the whirlwind:ready label before automatic execution.
func (i Issue) Eligible() bool {
	if i.State != "OPEN" && i.State != "open" {
		return false
	}
	return slices.Contains(i.Labels, "whirlwind:ready")
}
