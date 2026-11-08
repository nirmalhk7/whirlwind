package workflow

import (
	"strings"
	"testing"
)

func TestValidateRoleOutputRejectsMalformedPlannerAndReviewAndFinalizer(t *testing.T) {
	for _, test := range []struct {
		role, purpose, text string
	}{
		{role: "plan", text: `{"objective":"x","acceptance_criteria":["x"]}`},
		{role: "review", purpose: "primary", text: `{"summary":" ","findings":[]}`},
		{role: "finalizer", purpose: "local_delivery_summary", text: "  "},
	} {
		if err := validateRoleOutput(test.role, test.purpose, test.text); err == nil {
			t.Errorf("accepted malformed %s output", test.role)
		}
	}
}

func TestValidateRoleOutputKeepsLowAdherenceReviewValid(t *testing.T) {
	text := `{"summary":"Requirement remains unverified; evidence is missing.","findings":[],"requirements":[{"id":"REQ-1","status":"unverified","explanation":"No test evidence was provided."}]}`
	if err := validateRoleOutput("review", "primary", text); err != nil {
		t.Fatalf("valid low-adherence review entered malformed-output recovery: %v", err)
	}
}

func TestProviderMetadataReplyIsNotValidatedAsProductiveRoleOutput(t *testing.T) {
	if err := validateRoleOutput("plan", "provider_test", "Hello World"); err != nil {
		t.Fatalf("provider metadata reply entered planner JSON recovery: %v", err)
	}
	if err := validateRoleOutput("plan", "primary", "Hello World"); err == nil {
		t.Fatal("productive planner output bypassed the required JSON schema")
	}
}

func TestValidateRoleOutputReportsPlannerSchemaError(t *testing.T) {
	err := validateRoleOutput("plan", "", `{"objective":"x","what":"Fix it","why":"It fails","benefit":"It works","where":["a.go"],"exclusions":["Keep the API stable."],"steps":["Add a regression."],"acceptance_criteria":["x"]}`)
	if err == nil || !strings.Contains(err.Error(), "scope") {
		t.Fatalf("planner validation omitted actionable schema error: %v", err)
	}
}

func TestValidateRoleOutputRejectsMalformedIdeationAndEmptyCode(t *testing.T) {
	for _, test := range []struct {
		role, purpose, text string
	}{
		{role: "ideate", purpose: "generation", text: `{"directions":[]}`},
		{role: "ideate", purpose: "novelty:check", text: `{"reason":"unclear"}`},
		{role: "ideate", purpose: "integration_context", text: `{"sources":[]}`},
		{role: "code", purpose: "primary", text: "  "},
	} {
		if err := validateRoleOutput(test.role, test.purpose, test.text); err == nil {
			t.Errorf("accepted malformed %s/%s output", test.role, test.purpose)
		}
	}
}

func TestValidateRoleOutputAllowsPartialDirectionsAndNoveltyRepair(t *testing.T) {
	if err := validateRoleOutput("ideate", "generation", `[]`); err != nil {
		t.Fatalf("empty direction batch should remain an incomplete repair input: %v", err)
	}
	if err := validateRoleOutput("ideate", "generation", `[{"category":"optimize","title":"Improve cache","description":"Reduce repeated reads"}]`); err != nil {
		t.Fatalf("valid partial direction batch should remain a repair input: %v", err)
	}
	if err := validateRoleOutput("ideate", "novelty:check", `{"unique":false,"reason":"duplicate","duplicate_categories":["optimize"]}`); err != nil {
		t.Fatalf("valid duplicate verdict should remain a correction-loop input: %v", err)
	}
}
