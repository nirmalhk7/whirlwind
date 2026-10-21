package model

// Lesson is advisory experience owned by one role in one target project.
type Lesson struct {
	Secret         bool              `json:"secret,omitempty"`
	IsSecret       bool              `json:"isSecret,omitempty"`
	ID             string            `json:"id"`
	ProjectID      string            `json:"project_id"`
	Role           string            `json:"role"`
	Trigger        string            `json:"trigger"`
	Mistake        string            `json:"mistake"`
	Correction     string            `json:"correction"`
	Paths          []string          `json:"paths,omitempty"`
	Symbols        []string          `json:"symbols,omitempty"`
	ErrorSignature string            `json:"error_signature,omitempty"`
	EvidenceIDs    []string          `json:"evidence_ids,omitempty"`
	Anchors        map[string]string `json:"anchors,omitempty"`
	Status         string            `json:"status"`
	AttemptID      string            `json:"attempt_id"`
	TaskID         string            `json:"task_id"`
	Provider       string            `json:"provider"`
	Model          string            `json:"model,omitempty"`
	Reason         string            `json:"reason,omitempty"`
}

type LessonFeedback struct {
	Secret      bool              `json:"secret,omitempty"`
	IsSecret    bool              `json:"isSecret,omitempty"`
	Paths       []string          `json:"paths,omitempty"`
	Anchors     map[string]string `json:"-"`
	LessonID    string            `json:"lesson_id"`
	Verdict     string            `json:"verdict"`
	EvidenceIDs []string          `json:"evidence_ids,omitempty"`
	Reason      string            `json:"reason"`
}

type LearningReport struct {
	Lessons  []Lesson         `json:"lessons,omitempty"`
	Feedback []LessonFeedback `json:"feedback,omitempty"`
}

type LessonRef struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	Status  string `json:"status"`
}

// LearningSnapshot records exactly what this attempt saw and submitted.
type LearningSnapshot struct {
	Recalled        []LessonRef      `json:"recalled"`
	CreatedIDs      []string         `json:"created_ids,omitempty"`
	Feedback        []LessonFeedback `json:"feedback,omitempty"`
	Warnings        []string         `json:"warnings,omitempty"`
	Bytes           int              `json:"bytes"`
	EstimatedTokens int              `json:"estimated_tokens"`
}
