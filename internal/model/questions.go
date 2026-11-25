package model

type Question struct {
	Options []string `json:"options,omitempty"`
	Secret  bool     `json:"secret,omitempty"`
	ID      string   `json:"id"`
	Text    string   `json:"text"`
	Answer  string   `json:"answer"`
}

type QuestionSet struct {
	AttemptID   string            `json:"attempt_id,omitempty"`
	ID          string            `json:"id"`
	TaskID      string            `json:"plan_id"`
	Provider    string            `json:"provider"`
	Questions   []Question        `json:"questions"`
	Answers     map[string]string `json:"answers"`
	Status      string            `json:"status"`
	PlanVersion int               `json:"plan_version"`
}
