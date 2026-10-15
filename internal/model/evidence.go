package model

import "encoding/json"

// Evidence payloads live outside resource snapshots so SSE never duplicates content.
type Artifact struct {
	OwnerKind   string `json:"owner_kind"`
	ID          string `json:"id"`
	AttemptID   string `json:"attempt_id"`
	Kind        string `json:"kind"`
	ContentType string `json:"content_type"`
	Bytes       int    `json:"bytes"`
	StoredBytes int    `json:"stored_bytes"`
	SHA256      string `json:"sha256"`
	Truncated   bool   `json:"truncated"`
	CreatedAt   string `json:"created_at"`
}
type AttemptEvent struct {
	Metadata  json.RawMessage `json:"metadata"`
	Sequence  int64           `json:"sequence"`
	AttemptID string          `json:"attempt_id"`
	Kind      string          `json:"kind"`
	Artifact  *Artifact       `json:"artifact,omitempty"`
	Replay    bool            `json:"replay"`
	CreatedAt string          `json:"created_at"`
}
type Usage struct {
	InputTokens  *int64   `json:"input_tokens"`
	OutputTokens *int64   `json:"output_tokens"`
	CachedTokens *int64   `json:"cached_tokens"`
	TotalTokens  *int64   `json:"total_tokens"`
	Cost         *float64 `json:"cost"`
	Scope        string   `json:"scope"`
}
type Operation struct {
	Kind        string `json:"kind"`
	Subject     string `json:"subject"`
	Owner       string `json:"owner"`
	Stage       string `json:"stage"`
	Input       any    `json:"input,omitempty"`
	StartedAt   string `json:"started_at"`
	FinishedAt  string `json:"finished_at,omitempty"`
	Error       string `json:"error,omitempty"`
	FailureKind string `json:"failure_kind,omitempty"`
}
type Provenance struct {
	Task           string   `json:"task"`
	PlanVersion    int      `json:"plan_version"`
	Head           string   `json:"head"`
	Base           string   `json:"base"`
	PolicyHash     string   `json:"policy_hash"`
	VerificationID string   `json:"verification_id,omitempty"`
	ReviewID       string   `json:"review_id,omitempty"`
	AttemptIDs     []string `json:"attempt_ids,omitempty"`
}
type Audit struct {
	Task      string      `json:"task,omitempty"`
	AttemptID string      `json:"attempt_id,omitempty"`
	Source    string      `json:"source"`
	Action    string      `json:"action"`
	Target    string      `json:"target"`
	Version   int         `json:"version"`
	Reason    string      `json:"reason,omitempty"`
	CreatedAt string      `json:"created_at"`
	Evidence  *Provenance `json:"evidence,omitempty"`
}
type Publication struct {
	PolicyHash  string      `json:"policy_hash"`
	Evidence    *Provenance `json:"evidence,omitempty"`
	Kind        string      `json:"kind"`
	Task        string      `json:"task"`
	PlanVersion int         `json:"plan_version"`
	Repo        string      `json:"repo"`
	Branch      string      `json:"branch,omitempty"`
	Base        string      `json:"base,omitempty"`
	Head        string      `json:"head,omitempty"`
	Title       string      `json:"title"`
	Body        string      `json:"body"`
	Fingerprint string      `json:"fingerprint"`
	Stage       string      `json:"stage"`
	Owner       string      `json:"owner"`
	StartedAt   string      `json:"started_at"`
	FinishedAt  string      `json:"finished_at,omitempty"`
	URL         string      `json:"url,omitempty"`
	Issue       *Issue      `json:"issue,omitempty"`
	Error       string      `json:"error,omitempty"`
}

// Attempt preserves additive session fields while distinguishing reported runtime metadata from configured input.
type Attempt struct {
	AttemptID           string          `json:"attempt_id"`
	Task                string          `json:"task"`
	ProjectID           string          `json:"project_id"`
	UnitID              string          `json:"unit_id,omitempty"`
	OperationID         string          `json:"operation_id,omitempty"`
	Purpose             string          `json:"purpose,omitempty"`
	PreviousAttemptID   string          `json:"previous_attempt_id,omitempty"`
	Role                string          `json:"role"`
	Provider            string          `json:"provider"`
	ProviderName        string          `json:"provider_name"`
	SessionID           string          `json:"session_id"`
	RequestedSessionID  string          `json:"requested_session_id"`
	Resumed             *bool           `json:"resumed,omitempty"`
	CWD                 string          `json:"cwd"`
	PlanVersion         int             `json:"plan_version"`
	Finished            bool            `json:"finished"`
	Status              string          `json:"status"`
	Owner               string          `json:"owner"`
	ContextCommit       string          `json:"context_commit"`
	ContextIndexVersion int             `json:"context_index_version"`
	PolicyHash          string          `json:"policy_hash"`
	Model               string          `json:"model"`
	ResolvedModel       string          `json:"resolved_model,omitempty"`
	Transport           string          `json:"transport"`
	Thinking            *ThinkingChoice `json:"thinking"`
	EffectiveThinking   *ThinkingChoice `json:"effective_thinking,omitempty"`
	StartedAt           string          `json:"started_at"`
	FinishedAt          string          `json:"finished_at,omitempty"`
	ElapsedMS           *int64          `json:"elapsed_ms,omitempty"`
	Success             *bool           `json:"success,omitempty"`
	Error               string          `json:"error,omitempty"`
	Summary             string          `json:"summary,omitempty"`
	PromptArtifact      string          `json:"prompt_artifact,omitempty"`
	InputArtifact       string          `json:"input_artifact,omitempty"`
	OutputArtifact      string          `json:"output_artifact,omitempty"`
	Usage               *Usage          `json:"usage,omitempty"`
}
type AttemptInput struct {
	Handoff             Handoff         `json:"handoff"`
	Policy              Policy          `json:"policy"`
	Provider            Provider        `json:"provider"`
	ReadOnly            bool            `json:"read_only"`
	ContextCommit       string          `json:"context_commit"`
	ContextIndexVersion int             `json:"context_index_version"`
	Model               string          `json:"model"`
	Thinking            *ThinkingChoice `json:"thinking"`
}
