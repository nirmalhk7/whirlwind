package context

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPackageDoesNotLeakVectorsOrBreakUnicode(t *testing.T) {
	idx := Index{Commit: "abc", Version: 1, Files: map[string]Entry{"a.go": {Path: "a.go", Text: "needle" + strings.Repeat("界", 5000), Vector: []float64{0.12345, 0.6789}}}}
	raw := idx.Package("needle")
	if strings.Contains(raw, "\"vector\"") {
		t.Fatal("retrieval vectors entered inference context")
	}
	if !utf8.ValidString(raw) || strings.Contains(raw, "�") {
		t.Fatal("source excerpt split a UTF-8 rune")
	}
	var pack map[string]any
	if err := json.Unmarshal([]byte(raw), &pack); err != nil {
		t.Fatal(err)
	}
}

func TestContextPackPreservesRequiredInputAndBoundsOptionalSources(t *testing.T) {
	idx := Index{Commit: "a", Version: 1, Files: map[string]Entry{"main.go": {Path: "main.go", Text: "Login " + strings.Repeat("界", 3000), Vector: []float64{.4}}, "test.go": {Path: "test.go", Text: "Login test", Tests: []string{"TestLogin"}}}}
	request := PackRequest{Required: "Must preserve acceptance: Login fails closed.", Query: "Login", Role: "review", PolicyHash: "policy-a", MaxInputTokens: 1600}
	pack, err := idx.Pack(request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pack.Prompt, request.Required) || len(pack.Prompt) > 1600 {
		t.Fatalf("required context lost or limit exceeded: %d", len(pack.Prompt))
	}
	if strings.Contains(pack.Prompt, "\"vector\"") || strings.Contains(pack.Prompt, "�") {
		t.Fatal("invalid source payload")
	}
	if len(pack.Omitted) == 0 || pack.CountMethod != "utf8_bytes_upper_estimate" {
		t.Fatalf("missing omission/measurement provenance: %+v", pack)
	}
	again, err := idx.Pack(request)
	if err != nil || pack.ID != again.ID || pack.Prompt != again.Prompt {
		t.Fatal("nondeterministic pack")
	}
	idx.Commit = "b"
	changed, _ := idx.Pack(request)
	if changed.ID == pack.ID {
		t.Fatal("revision did not invalidate pack")
	}
	request.PolicyHash = "policy-b"
	changed2, _ := idx.Pack(request)
	if changed2.ID == changed.ID {
		t.Fatal("policy did not invalidate pack")
	}
	request.MaxInputTokens = 10
	if _, err = idx.Pack(request); err == nil {
		t.Fatal("silently truncated required instructions")
	}
}

func TestContextPackRecordsOmissionsEvenWhenReferencesDoNotFitPrompt(t *testing.T) {
	path := strings.Repeat("long-directory/", 50) + "file.go"
	idx := Index{Commit: "c", Files: map[string]Entry{path: {Path: path, Text: "needle source"}}}
	pack, err := idx.Pack(PackRequest{Required: "mandatory", Query: "needle", MaxInputTokens: 220})
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.Omitted) != 1 || pack.Omitted[0] != path {
		t.Fatal("stored pack silently lost omitted source provenance")
	}
	if len(pack.Prompt) > 220 {
		t.Fatal("omission metadata exceeded prompt bound")
	}
}
