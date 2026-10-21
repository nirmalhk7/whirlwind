package workflow

import (
	"testing"
)

func TestLearningSchemaRejectsMalformedMetadataWithoutChangingResult(t *testing.T) {
	metadata := []string{
		`null`,
		`{"lessons":[{"trigger":null,"mistake":"guessing","correction":"read source"}]}`,
		`{"lessons":[{"trigger":"policy","mistake":"guessing"}]}`,
		`{"feedback":[{"lesson_id":"l","verdict":"invented","reason":"guess"}]}`,
	}
	for _, learning := range metadata {
		result, report, warnings := decodeLearning(`{"result":{"objective":"ok"},"learning":` + learning + `}`)
		if result != `{"objective":"ok"}` {
			t.Fatalf("primary result changed: %s", result)
		}
		if len(report.Lessons) != 0 || len(report.Feedback) != 0 || len(warnings) != 1 {
			t.Fatalf("invalid metadata accepted: %+v %v", report, warnings)
		}
	}
}

func TestLearningSchemaAllowsOptionalEmptyMetadataAndExtensions(t *testing.T) {
	for _, learning := range []string{`{}`, `{"lessons":null,"feedback":null}`, `{"future_field":{"enabled":true}}`} {
		result, _, warnings := decodeLearning(`{"result":"done","learning":` + learning + `}`)
		if result != "done" || len(warnings) != 0 {
			t.Fatalf("compatible metadata rejected: %s %v", result, warnings)
		}
	}
}
