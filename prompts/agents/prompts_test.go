package agents

import (
	"strings"
	"testing"
)

// Catch missing context, wrong template selection, and accidental interpretation of task text.
func TestRenderContext(t *testing.T) {
	literal := "<tag> {{.Unknown}} 100% & \"quoted\"\nnext line\n"
	const renderedPlan = "Plan:\nplan"
	cases := []struct {
		name   string
		data   map[string]string
		want   []string
		absent string
	}{
		{"ideate.md", map[string]string{"Index": literal, "Sources": "sources", "Previous": "previous"}, []string{"Repository index:\n" + literal, "GitHub sources:\nsources", "Previous suggestions:\nprevious"}, ""},
		{"ideate_novelty.md", map[string]string{"Candidates": literal, "Previous": "previous"}, []string{"Candidate ideas:\n" + literal, "Previous suggestions:\nprevious"}, ""},
		{"plan.md", map[string]string{"Title": literal, "Body": "body"}, []string{"Issue:\n" + literal + "\nbody"}, "Answered questions:"},
		{"plan.md", map[string]string{"Title": "title", "Body": "body", "Questions": literal}, []string{"Answered questions: " + literal}, ""},
		{"implement", map[string]string{"Unit": literal, "Plan": "plan", "Handoff": "handoff"}, []string{"Unit:\n" + literal, renderedPlan, "Handoff:\nhandoff"}, "Findings:"},
		{"repair", map[string]string{"Plan": "plan", "Findings": literal, "Handoff": "handoff"}, []string{renderedPlan, "Findings:\n" + literal, "Handoff:\nhandoff"}, "Unit:"},
		{"integrate", map[string]string{"Unit": literal}, []string{"Unit:\n" + literal}, "Findings:"},
		{"review.md", map[string]string{"Plan": "plan", "Previous": "previous", "Diff": literal}, []string{renderedPlan, "Previous findings:\nprevious", "Diff:\n" + literal}, "Perform dedicated"},
		{"specialist", map[string]string{"Prompt": literal, "Perspective": "security"}, []string{literal + "\nPerform dedicated security review; return same JSON contract."}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Render(c.name, c.data)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range c.want {
				if !strings.Contains(got, want) {
					t.Errorf("missing context %q in %q", want, got)
				}
			}
			if c.absent != "" && strings.Contains(got, c.absent) {
				t.Errorf("unexpected context %q", c.absent)
			}
		})
	}
}

func TestRenderUnknownPrompt(t *testing.T) {
	if _, err := Render("missing", nil); err == nil {
		t.Fatal("unknown template must return error")
	}
}
