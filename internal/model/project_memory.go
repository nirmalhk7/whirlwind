package model

// RoleLesson is a local operator's note linked to one completed agent attempt.
// Its project and role are the scope of the enclosing SQLite record.
type RoleLesson struct {
	AttemptID    string `json:"attempt_id"`
	PlanID       string `json:"plan_id,omitempty"`
	PlanVersion  int    `json:"plan_version,omitempty"`
	OperationID  string `json:"operation_id,omitempty"`
	SourcePolicy string `json:"source_policy,omitempty"`
	SourceCommit string `json:"source_commit,omitempty"`
	RecordedAt   string `json:"recorded_at"`
	Note         string `json:"note"`
}
