package provider

import (
	"encoding/json"
	"time"
)

type quotaWindow struct {
	Used   *float64 `json:"usedPercent"`
	Resets *int64   `json:"resetsAt"`
}
type quotaLimit struct {
	Primary   *quotaWindow `json:"primary"`
	Secondary *quotaWindow `json:"secondary"`
}

// Quota interprets Codex quota windows without inventing missing values and returns the latest known exhausted-window reset time.
func Quota(data any) (string, string, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return "unknown", "", err
	}
	var v struct {
		Limits map[string]quotaLimit `json:"rateLimitsByLimitId"`
		Limit  *quotaLimit           `json:"rateLimits"`
	}
	if err = json.Unmarshal(b, &v); err != nil {
		return "unknown", "", err
	}
	if len(v.Limits) == 0 && v.Limit != nil {
		v.Limits = map[string]quotaLimit{"default": *v.Limit}
	}
	state := "unknown"
	var reset int64
	unknownReset := false
	for _, limit := range v.Limits {
		for _, window := range []*quotaWindow{limit.Primary, limit.Secondary} {
			if window == nil || window.Used == nil {
				continue
			}
			if state == "unknown" {
				state = "available"
			}
			if *window.Used >= 100 {
				state = "exhausted"
				if window.Resets == nil || *window.Resets <= 0 {
					unknownReset = true
				} else if *window.Resets > reset {
					reset = *window.Resets
				}
			}
		}
	}
	if reset > 0 && !unknownReset {
		return state, time.Unix(reset, 0).UTC().Format(time.RFC3339), nil
	}
	return state, "", nil
}
