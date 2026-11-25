package model

type Provider struct {
	NativeIntegrations bool           `json:"-"`
	Sandbox            *SandboxPolicy `json:"sandbox,omitempty" yaml:"sandbox"`
	QuotaPool          string         `json:"quota_pool,omitempty"`
	QuotaSnapshot      *QuotaSnapshot `json:"quota_snapshot,omitempty"`
	ModelCommand       []string       `json:"model_command,omitempty"`
	ACPProfile         string         `json:"acp_profile,omitempty"`
	ID                 string         `json:"id"`
	Name               string         `json:"name"`
	Kind               string         `json:"kind"`
	Command            []string       `json:"command"`
	Capabilities       []string       `json:"capabilities"`
	Available          bool           `json:"available"`
	Quota              string         `json:"quota"`
	ResetAt            string         `json:"reset_at,omitempty"`
	Cost               *float64       `json:"cost,omitempty"`
	Limitation         string         `json:"limitation,omitempty"`
}
