package dashboard

import (
	"fmt"
	"strings"

	"whirlwind/internal/model"
)

func directionRationale(direction model.Direction) string {
	value := func(text string) string {
		if strings.TrimSpace(text) == "" {
			return "Not recorded"
		}
		return strings.TrimSpace(text)
	}
	var text strings.Builder
	fmt.Fprintf(&text, "%s · %s", direction.Title, direction.Category)
	writeField := func(label, content string) {
		if strings.TrimSpace(content) != "" {
			fmt.Fprintf(&text, "\n\n%s:\n%s", label, strings.TrimSpace(content))
		}
	}
	writeField("Description", direction.Description)
	writeField("Problem", value(direction.Problem))
	writeField("Approach", value(direction.Mechanism))
	writeField("Benefit", value(direction.ExpectedBenefit))
	writeField("Scope", strings.Join(direction.Scope, "\n"))
	writeField("Effort estimate", direction.EffortEstimate)
	writeField("Uncertainty", strings.Join(direction.Uncertainty, "\n"))
	if len(direction.Components) > 0 {
		writeField("Components", strings.Join(direction.Components, "\n"))
	}
	if len(direction.Evidence) > 0 {
		text.WriteString("\n\nEvidence:\n")
		for _, evidence := range direction.Evidence {
			text.WriteString(evidence.Kind + ": " + evidence.Ref)
			if evidence.Summary != "" {
				text.WriteString(" — " + evidence.Summary)
			}
			if evidence.URL != "" {
				text.WriteString(" (" + evidence.URL + ")")
			}
			text.WriteByte('\n')
		}
	}
	return text.String()
}
