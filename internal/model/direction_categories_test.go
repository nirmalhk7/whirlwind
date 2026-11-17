package model

import (
	"reflect"
	"testing"
)

func TestDirectionCategoriesPreserveOrderAndCallerIsolation(t *testing.T) {
	want := []string{"expand", "optimize", "harden", "simplify"}
	first := DirectionCategories()
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("category contract changed: %v", first)
	}
	first[0] = "other"
	if !reflect.DeepEqual(DirectionCategories(), want) {
		t.Fatal("caller modified the shared category contract")
	}
	for _, category := range want {
		if !IsDirectionCategory(category) {
			t.Fatalf("valid category rejected: %q", category)
		}
	}
	for _, category := range []string{"", "Expand", "expand ", "other"} {
		if IsDirectionCategory(category) {
			t.Fatalf("invalid category accepted: %q", category)
		}
	}
}
