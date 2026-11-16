package policy

import (
	"testing"
	"whirlwind/internal/model"
)

func TestParityVerificationModeMustBeSupportedBeforeActivation(t *testing.T) {
	for _, risk := range []bool{false, true} {
		p := model.Policy{Checks: []model.Check{{Name: "test", Command: []string{"/usr/bin/true"}}}, AllowedPaths: []string{"**"}, RiskRules: []model.RiskRule{{Level: "high", Paths: []string{"**"}}}}
		bad := model.Check{Name: "bad", Command: []string{"/usr/bin/true"}, Mode: "unsafe-guess"}
		if risk {
			p.RiskRules[0].Checks = []model.Check{bad}
		} else {
			p.Checks = append(p.Checks, bad)
		}
		if err := Validate(p); err == nil {
			t.Errorf("unsupported command mode activated: risk=%v", risk)
		}
	}
}
