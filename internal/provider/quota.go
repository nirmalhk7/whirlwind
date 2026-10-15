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
	quota := quotaStatus{state: "unknown"}
	for _, limit := range v.Limits {
		quota.add(limit.Primary)
		quota.add(limit.Secondary)
	}
	if quota.reset > 0 && !quota.unknownReset {
		return quota.state, time.Unix(quota.reset, 0).UTC().Format(time.RFC3339), nil
	}
	return quota.state, "", nil
}

type quotaStatus struct {
	state        string
	reset        int64
	unknownReset bool
}

func (q *quotaStatus) add(window *quotaWindow) {
	if window == nil || window.Used == nil {
		return
	}
	if q.state == "unknown" {
		q.state = "available"
	}
	if *window.Used >= 100 {
		q.state = "exhausted"
		if window.Resets == nil || *window.Resets <= 0 {
			q.unknownReset = true
		} else if *window.Resets > q.reset {
			q.reset = *window.Resets
		}
	}
}
