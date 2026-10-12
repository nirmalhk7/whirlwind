package workflow

import (
	"testing"
	"time"

	"khan/internal/model"
)

// TestAcceptanceRequiresChecksAndNoBlockers verifies a high review score cannot bypass failed checks or unresolved blocking findings.
func TestAcceptanceRequiresChecksAndNoBlockers(t *testing.T) {
	for _, c := range []struct {
		score    float64
		checks   bool
		blocking bool
		want     bool
	}{{9, true, false, true}, {8.9, true, false, false}, {10, false, false, false}, {10, true, true, false}} {
		r := model.Review{Score: c.score}
		if c.blocking {
			r.Findings = []model.Finding{{Blocking: true}}
		}
		if Accepted(r, c.checks) != c.want {
			t.Fatalf("acceptance %+v", c)
		}
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
