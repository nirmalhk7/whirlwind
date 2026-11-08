package model

type Requirement struct {
	ID             string `json:"id"`
	Outcome        string `json:"outcome"`
	EvidenceMethod string `json:"evidence_method"`
}

type RequirementResult struct {
	ID          string   `json:"id"`
	Status      string   `json:"status"`
	Code        []string `json:"code"`
	Evidence    []string `json:"evidence"`
	Explanation string   `json:"explanation"`
}

type Adherence struct {
	Score      float64             `json:"score"`
	Passed     int                 `json:"passed"`
	Total      int                 `json:"total"`
	Accepted   bool                `json:"accepted"`
	Results    []RequirementResult `json:"results"`
	Provenance Provenance          `json:"provenance"`
}
