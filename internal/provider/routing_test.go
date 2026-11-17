package provider

import (
	"testing"

	"whirlwind/internal/model"
)

// TestRoutingRejectsIncapableAndExhaustedProviders verifies routing excludes unavailable, exhausted, and role-incompatible providers.
func TestRoutingRejectsIncapableAndExhaustedProviders(t *testing.T) {
	ps := []model.Provider{{ID: "cheap", Available: true, Quota: "exhausted", Capabilities: []string{"code"}}, {ID: "read", Available: true, Quota: "unknown", Capabilities: []string{"ideate"}}, {ID: "good", Available: true, Quota: "unknown", Capabilities: []string{"code"}}}
	p, e := Route(ps, "code", nil, nil)
	if e != nil || p.ID != "good" {
		t.Fatalf(valueErrorFormat, p, e)
	}
}
