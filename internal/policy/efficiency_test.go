package policy

import (
	"testing"
	"whirlwind/internal/model"
)

func TestEfficiencySettingsRejectInvalidBounds(t *testing.T) {
	valid := model.Policy{Checks: []model.Check{{Name: "test", Command: []string{"true"}}}, AllowedPaths: []string{"**"}, RiskRules: []model.RiskRule{{Level: "low", Paths: []string{"**"}}}}
	p := valid
	p.ContextInputTokens = -1
	if Validate(p) == nil {
		t.Fatal("negative context allowance accepted")
	}
	p = valid
	p.Checks = []model.Check{{Name: "test", Command: []string{"true"}, Reusable: true}}
	if Validate(p) == nil {
		t.Fatal("reusable check without declared freshness accepted")
	}
	p.Checks[0].FreshnessSeconds = 60
	if err := Validate(p); err != nil {
		t.Fatal(err)
	}
	p.Checks[0].FreshnessSeconds = -1
	if Validate(p) == nil {
		t.Fatal("negative freshness accepted")
	}
}
