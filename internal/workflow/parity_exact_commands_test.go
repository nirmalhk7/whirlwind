package workflow

import (
	"reflect"
	"testing"

	"whirlwind/internal/model"
)

func TestParityVerificationDoesNotRewriteApprovedArgv(t *testing.T) {
	approved := []model.Check{{Name: "test", Command: []string{"go", "test", "./..."}}, {Name: "lint", Command: []string{"qlty", "check", "src/new.go"}}}
	actual := structuredChecks(approved)
	if !reflect.DeepEqual(actual, approved) {
		t.Fatalf("approved argv changed: %#v", actual)
	}
}
