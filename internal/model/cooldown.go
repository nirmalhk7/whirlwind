package model

type ProviderCooldown struct {
	Pool      string `json:"pool"`
	AttemptID string `json:"attempt_id"`
	Until     string `json:"until"`
	Reason    string `json:"reason"`
}
