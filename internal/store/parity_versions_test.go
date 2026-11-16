package store

import (
	"context"
	"errors"
	"testing"
)

func TestParityApprovedSpecificationVersionsAreImmutable(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.Put(ctx, "execution_plans", "plan", map[string]any{"id": "plan", "project_id": "project", "plan_version": 1, "stage": "plan_approval"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "plan_versions", "plan:1", map[string]string{"specification": "Approved bytes\n"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "plan_versions", "plan:1", map[string]string{"specification": "Replaced"}, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("approved version was overwritten: %v", err)
	}
}
