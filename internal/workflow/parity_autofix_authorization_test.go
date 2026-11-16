package workflow

import (
	"testing"
	"whirlwind/internal/model"
)

func TestParityPlanCannotGrantSourceWritesToVerificationCommand(t *testing.T) {
	human := model.Policy{Checks: []model.Check{{Name: "quality", Mode: "verify", Command: []string{"qlty", "check", "--upstream", "HEAD", "--fix", "--no-progress", "--sarif"}}}}
	plan := model.PlanSpecification{Checks: []model.Check{{Name: "quality", Mode: "autofix", Command: []string{"qlty", "check", "--upstream", "HEAD", "--fix", "--no-progress", "--sarif"}}}}
	if err := validatePlanCommandReferences(human, plan); err == nil {
		t.Fatal("agent-authored plan expanded human command's source-write authority")
	}
}
