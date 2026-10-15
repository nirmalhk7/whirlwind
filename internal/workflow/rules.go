package workflow

import (
	"time"

	"whirlwind/internal/model"
)

// Accepted requires passing checks, a score from 9 through 10, and no unresolved blocking findings.
func Accepted(r model.Review, checks bool) bool {
	if !checks || r.Score < 9 || r.Score > 10 {
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
