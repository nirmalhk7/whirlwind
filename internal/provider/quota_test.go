package provider

import (
	"encoding/json"
	"testing"
)

// TestQuotaKeepsMissingWindowsUnknown verifies missing quota data remains unknown and exhausted windows retain reported reset information.
func TestQuotaKeepsMissingWindowsUnknown(t *testing.T) {
	for _, c := range []struct{ body, want, reset string }{{`{"rateLimitsByLimitId":{"codex":{"primary":null,"secondary":null}}}`, "unknown", ""}, {`{"rateLimits":{"primary":{"usedPercent":40,"resetsAt":1900000000}}}`, "available", ""}, {`{"rateLimits":{"primary":{"usedPercent":100,"resetsAt":null}}}`, "exhausted", ""}} {
		state, reset, err := Quota(json.RawMessage(c.body))
		if err != nil || state != c.want || reset != c.reset {
			t.Fatalf("%s %s %v", state, reset, err)
		}
	}
}
