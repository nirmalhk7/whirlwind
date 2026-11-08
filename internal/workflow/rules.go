package workflow

import (
	"time"

	"whirlwind/internal/model"
)

// Accepted requires passing mandatory gates, computed requirement adherence above
// 80 percent, and no unresolved blocking findings.
func Accepted(r model.Review, checks bool) bool {
	if !checks || r.Adherence == nil || !r.Adherence.Accepted || r.Adherence.Score <= 80 || r.Adherence.Total == 0 || r.Adherence.Passed < 0 || r.Adherence.Passed > r.Adherence.Total {
		return false
	}
	if r.Adherence.Score != 100*float64(r.Adherence.Passed)/float64(r.Adherence.Total) {
		return false
	}
	for _, f := range r.Findings {
		if f.Blocking && !f.Resolved {
			return false
		}
	}
	return true
}

// DueDay returns the latest day whose 10AM run is due in the supplied time location.
func DueDay(now time.Time) string {
	if now.Hour() < 10 {
		now = now.AddDate(0, 0, -1)
	}
	return now.Format("2006-01-02")
}

// MergeFindings retains unresolved previous blockers and applies current findings or explicit resolutions by stable ID.
func MergeFindings(previous, current []model.Finding) []model.Finding {
	byID := map[string]model.Finding{}
	order := []string{}
	for _, f := range previous {
		if f.Blocking && !f.Resolved {
			byID[f.ID] = f
			order = append(order, f.ID)
		}
	}
	for _, f := range current {
		if _, ok := byID[f.ID]; !ok {
			order = append(order, f.ID)
		}
		byID[f.ID] = f
	}
	out := []model.Finding{}
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out
}
