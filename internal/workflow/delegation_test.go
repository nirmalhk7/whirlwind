package workflow

import (
	"reflect"
	"testing"
	"whirlwind/internal/model"
)

func TestDelegationSerializesUnknownAndOverlappingScopes(t *testing.T) {
	units := []model.Unit{{ID: "a", AllowedPaths: []string{"api/**"}}, {ID: "b", AllowedPaths: []string{"api/routes.go"}}, {ID: "c", AllowedPaths: []string{"web/**"}}, {ID: "d"}}
	ready := []int{0, 1, 2, 3}
	if got := selectUnitBatch(units, ready, 4, false); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("default must be serial: %v", got)
	}
	if got := selectUnitBatch(units, ready, 4, true); !reflect.DeepEqual(got, []int{0, 2}) {
		t.Fatalf("overlap or unbounded unit admitted: %v", got)
	}
	if got := selectUnitBatch(units, ready, 1, true); len(got) != 1 {
		t.Fatal("capacity exceeded")
	}
}

func TestUnitPlanDoesNotDuplicateOtherUnits(t *testing.T) {
	plan := model.Plan{Objective: "shared goal", Specification: "required contract", AcceptanceCriteria: []string{"must pass"}, Units: []model.Unit{{ID: "a", Objective: "first"}, {ID: "b", Objective: "second"}}}
	scoped := planForUnit(plan, 0)
	if len(scoped.Units) != 1 || scoped.Units[0].ID != "a" || scoped.Objective != "shared goal" || scoped.AcceptanceCriteria[0] != "must pass" {
		t.Fatalf("bad scoped plan: %+v", scoped)
	}
}
