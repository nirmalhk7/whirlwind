package model

type Project struct {
	OfflineQualification *OfflineDockerQualification `json:"offline_qualification,omitempty"`
	Status               string                      `json:"status,omitempty"`
	RemoteURL            string                      `json:"remote_url,omitempty"`
	Paused               bool                        `json:"paused"`
	DefaultBranch        string                      `json:"default_branch"`
	ID                   string                      `json:"id"`
	CheckoutAliases      []string                    `json:"checkout_aliases,omitempty"`
	Name                 string                      `json:"name"`
	Repo                 string                      `json:"repo"`
	Path                 string                      `json:"path"`
	Policy               Policy                      `json:"policy"`
	PolicyHash           string                      `json:"policy_hash"`
	ManagedPolicy        bool                        `json:"managed_policy,omitempty"`
	QualityIdentity      *QualityIdentity            `json:"quality_identity,omitempty"`
	WebhookURL           string                      `json:"webhook_url,omitempty"`
}

type OfflineDockerQualification struct {
	PolicyHash string `json:"policy_hash"`
	JobID      string `json:"job_id"`
	ImageID    string `json:"image_id"`
}

type ProjectPolicyDocument struct {
	ProjectID string `json:"project_id"`
	Content   string `json:"content"`
}
