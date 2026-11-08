package workflow

import (
	"encoding/json"
	"testing"
	"time"

	"whirlwind/internal/model"
)

// TestAcceptanceRequiresChecksAndNoBlockers verifies a high review score cannot bypass failed checks or unresolved blocking findings.
func TestAcceptanceRequiresChecksAndNoBlockers(t *testing.T) {
	for _, c := range []struct {
		score    float64
		adheres  bool
		checks   bool
		blocking bool
		want     bool
	}{{9, true, true, false, true}, {8.9, false, true, false, false}, {10, true, false, false, false}, {10, true, true, true, false}} {
		report := &model.Adherence{Score: 80, Passed: 4, Total: 5}
		if c.adheres {
			report = &model.Adherence{Score: 81, Passed: 81, Total: 100, Accepted: true}
		}
		r := model.Review{Score: c.score, Adherence: report}
		if c.blocking {
			r.Findings = []model.Finding{{Blocking: true}}
		}
		if Accepted(r, c.checks) != c.want {
			t.Fatalf("acceptance %+v", c)
		}
	}
}

func TestAcceptanceUsesComputedAdherencePercentage(t *testing.T) {
	var review model.Review
	if err := json.Unmarshal([]byte(`{"score":10,"adherence":{"score":80,"passed":4,"total":5,"accepted":true}}`), &review); err != nil {
		t.Fatal(err)
	}
	if Accepted(review, true) {
		t.Fatal("exactly 80 percent of frozen requirements passed")
	}
	if err := json.Unmarshal([]byte(`{"score":0,"adherence":{"score":81,"passed":81,"total":100,"accepted":true}}`), &review); err != nil {
		t.Fatal(err)
	}
	if !Accepted(review, true) {
		t.Fatal("computed adherence above 80 percent should pass despite the model's numeric score")
	}
}

// TestRejectCyclicAndUnknownDependencies verifies task graphs reject dependency cycles and missing unit references.
func TestRejectCyclicAndUnknownDependencies(t *testing.T) {
	for _, u := range [][]model.Unit{{{ID: "a", Dependencies: []string{"b"}}, {ID: "b", Dependencies: []string{"a"}}}, {{ID: "a", Dependencies: []string{"missing"}}}} {
		if ValidateUnits(u) == nil {
			t.Fatal("invalid graph accepted")
		}
	}
	if e := ValidateUnits([]model.Unit{{ID: "a"}, {ID: "b", Dependencies: []string{"a"}}}); e != nil {
		t.Fatal(e)
	}
}

// TestLatestDueScheduleAndDST verifies daily scheduling selects the latest due date across the 10AM boundary and daylight-saving changes.
func TestLatestDueScheduleAndDST(t *testing.T) {
	loc, e := time.LoadLocation("America/Denver")
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, loc)
	if DueDay(now) != "2026-10-31" {
		t.Fatal(DueDay(now))
	}
	now = now.Add(2 * time.Hour)
	if DueDay(now) != "2026-11-01" {
		t.Fatal(DueDay(now))
	}
}

// TestUnsafeUnitIDsRejected verifies path-unsafe and reserved implementation-unit identifiers are rejected.
func TestUnsafeUnitIDsRejected(t *testing.T) {
	for _, id := range []string{"../other", "integration", "a/b"} {
		if ValidateUnits([]model.Unit{{ID: id}}) == nil {
			t.Fatalf("unsafe id accepted: %s", id)
		}
	}
}

// TestOmittedBlockingFindingStaysUnresolved verifies reviewers must explicitly resolve previous blockers rather than silently omit them.
func TestOmittedBlockingFindingStaysUnresolved(t *testing.T) {
	previous := []model.Finding{{ID: "block", Blocking: true, Message: "permission bug"}}
	merged := MergeFindings(previous, nil)
	if len(merged) != 1 || !merged[0].Blocking || merged[0].Resolved {
		t.Fatal(merged)
	}
	merged = MergeFindings(previous, []model.Finding{{ID: "block", Resolved: true}})
	if len(merged) != 1 || !merged[0].Resolved {
		t.Fatal(merged)
	}
}
