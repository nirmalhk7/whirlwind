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
	Raw                  json.RawMessage `json:"raw,omitempty"`
	Source               string          `json:"source,omitempty"`
	Completeness         string          `json:"completeness,omitempty"`
	CachedInputSemantics string          `json:"cached_input_semantics,omitempty"`
	CacheWriteTokens     *int64          `json:"cache_write_tokens,omitempty"`
	InputTokens          *int64          `json:"input_tokens"`
	OutputTokens         *int64          `json:"output_tokens"`
	CachedTokens         *int64          `json:"cached_tokens"`
	TotalTokens          *int64          `json:"total_tokens"`
	Cost                 *float64        `json:"cost"`
	Scope                string          `json:"scope"`
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
	ExecutionID           string          `json:"execution_id,omitempty"`
	SandboxName           string          `json:"sandbox_name,omitempty"`
	SandboxID             string          `json:"sandbox_id,omitempty"`
	ExecutionLabelValue   string          `json:"execution_label_value,omitempty"`
	LifecycleFile         string          `json:"lifecycle_file,omitempty"`
	GatewayName           string          `json:"gateway_name,omitempty"`
	GatewayEndpoint       string          `json:"gateway_endpoint,omitempty"`
	Workspace             string          `json:"workspace,omitempty"`
	WorkerHeartbeatAt     string          `json:"worker_heartbeat_at,omitempty"`
	LeaseExpiresAt        string          `json:"lease_expires_at,omitempty"`
	ProviderProgressAt    string          `json:"provider_progress_at,omitempty"`
	ActiveTool            string          `json:"active_tool,omitempty"`
	ProcessState          string          `json:"process_state,omitempty"`
	SandboxHealth         string          `json:"sandbox_health,omitempty"`
	SandboxPhase          string          `json:"sandbox_phase,omitempty"`
	ConfigSnapshotHash    string          `json:"config_snapshot_hash,omitempty"`
	ConfigSnapshotVersion int             `json:"config_snapshot_version,omitempty"`
	ExecutionGeneration   int             `json:"execution_generation,omitempty"`
	ExecutionProfile      string          `json:"execution_profile,omitempty"`
	RuntimeKind           string          `json:"runtime_kind,omitempty"`
	RuntimeLauncher       string          `json:"runtime_launcher,omitempty"`
	AttemptID             string          `json:"attempt_id"`
	Task                  string          `json:"task"`
	ProjectID             string          `json:"project_id"`
	UnitID                string          `json:"unit_id,omitempty"`
	OperationID           string          `json:"operation_id,omitempty"`
	Purpose               string          `json:"purpose,omitempty"`
	ResumedFromAttemptID  string          `json:"resumed_from_attempt_id,omitempty"`
	PreviousAttemptID     string          `json:"previous_attempt_id,omitempty"`
	RecoveryEpisodeID     string          `json:"recovery_episode_id,omitempty"`
	RestartCount          int             `json:"restart_count,omitempty"`
	QueuePosition         int             `json:"queue_position,omitempty"`
	QueueLength           int             `json:"queue_length,omitempty"`
	Role                  string          `json:"role"`
	Provider              string          `json:"provider"`
	ProviderName          string          `json:"provider_name"`
	SessionID             string          `json:"session_id"`
	RequestedSessionID    string          `json:"requested_session_id"`
	Resumed               *bool           `json:"resumed,omitempty"`
	CWD                   string          `json:"cwd"`
	PlanVersion           int             `json:"plan_version"`
	Finished              bool            `json:"finished"`
	Status                string          `json:"status"`
	Owner                 string          `json:"owner"`
	ContextCommit         string          `json:"context_commit"`
	ContextIndexVersion   int             `json:"context_index_version"`
	PolicyHash            string          `json:"policy_hash"`
	Model                 string          `json:"model"`
	ResolvedModel         string          `json:"resolved_model,omitempty"`
	Transport             string          `json:"transport"`
	Thinking              *ThinkingChoice `json:"thinking"`
	EffectiveThinking     *ThinkingChoice `json:"effective_thinking,omitempty"`
	StartedAt             string          `json:"started_at"`
	DeadlineAt            string          `json:"deadline_at,omitempty"`
	FinishedAt            string          `json:"finished_at,omitempty"`
	ElapsedMS             *int64          `json:"elapsed_ms,omitempty"`
	Success               *bool           `json:"success,omitempty"`
	Error                 string          `json:"error,omitempty"`
	Summary               string          `json:"summary,omitempty"`
	PromptArtifact        string          `json:"prompt_artifact,omitempty"`
	InputArtifact         string          `json:"input_artifact,omitempty"`
	OutputArtifact        string          `json:"output_artifact,omitempty"`
	Usage                 *Usage          `json:"usage,omitempty"`
}

// RecoveryEpisode persists the restart budget for one role and configured queue entry.
type RecoveryEpisode struct {
	LastAttemptID string `json:"last_attempt_id,omitempty"`
	NextRetryAt   string `json:"next_retry_at,omitempty"`
	EpisodeID     string `json:"episode_id"`
	EntryID       string `json:"entry_id"`
	ProjectID     string `json:"project_id"`
	TaskID        string `json:"task_id"`
	Role          string `json:"role"`
	Purpose       string `json:"purpose,omitempty"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	ThinkingKey   string `json:"thinking_key,omitempty"`
	UnitID        string `json:"unit_id,omitempty"`
	PlanVersion   int    `json:"plan_version"`
	Generation    int    `json:"generation"`
	RestartCount  int    `json:"restart_count"`
	Status        string `json:"status"`
	FailureKind   string `json:"failure_kind,omitempty"`
	Error         string `json:"error,omitempty"`
	Owner         string `json:"owner,omitempty"`
	UpdatedAt     string `json:"updated_at"`
}
type AttemptInput struct {
	ExecutionProfile    string          `json:"execution_profile,omitempty"`
	Handoff             Handoff         `json:"handoff"`
	Policy              Policy          `json:"policy"`
	Provider            Provider        `json:"provider"`
	ReadOnly            bool            `json:"read_only"`
	ContextCommit       string          `json:"context_commit"`
	ContextIndexVersion int             `json:"context_index_version"`
	Model               string          `json:"model"`
	Thinking            *ThinkingChoice `json:"thinking"`
}
