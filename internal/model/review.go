package model

type Finding struct {
	ID       string `json:"id"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Message  string `json:"message"`
	Blocking bool   `json:"blocking"`
	Resolved bool   `json:"resolved"`
}

type Review struct {
	Adherence    *Adherence          `json:"adherence,omitempty"`
	Requirements []RequirementResult `json:"requirements,omitempty"`
	Provenance   *Provenance         `json:"provenance,omitempty"`
	Perspective  string              `json:"perspective,omitempty"`
	Score        float64             `json:"score"`
	Findings     []Finding           `json:"findings"`
	Summary      string              `json:"summary"`
}
