package workflow

import (
	"slices"
	"testing"

	"whirlwind/internal/model"
)

func TestRoleDispatchRotatesAfterPersistedRole(t *testing.T) {
	want := []string{ReviewerQueue, FinalizerQueue, PlannerQueue, CodingQueue, ""}
	if got := rotatedDispatchRoles(CodingQueue); !slices.Equal(got, want) {
		t.Fatalf("dispatch roles=%v, want %v", got, want)
	}
	first := model.Record{CreatedOrder: 10, UpdatedAt: "2026-11-01T07:00:00Z"}
	second := model.Record{CreatedOrder: 2, UpdatedAt: "2026-11-01T07:00:00.01Z"}
	if !roleQueueRowBefore(first, second) || roleQueueRowBefore(second, first) {
		t.Fatal("FIFO order followed original plan creation instead of revision time")
	}
}
