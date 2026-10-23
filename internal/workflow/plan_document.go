package workflow

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"whirlwind/internal/model"
)

var planDocumentList = regexp.MustCompile(`^(?:[-*+] |[0-9]+[.)] )(.+)$`)

// RenderPlanDocument produces an editable specification whose prose, criteria,
// and complete path scope are authoritative when the document is approved.
func RenderPlanDocument(plan model.Plan) string {
	if approved, err := ParsePlanDocument(plan.Specification); err == nil && sameDocumentPlan(approved, plan) {
		return strings.TrimSpace(plan.Specification) + "\n"
	}
	what, why, benefit := documentRationale(plan)
	paths := documentPaths(plan)
	var body strings.Builder
	fmt.Fprintf(&body, "# %s\n\n## What\n\n%s\n\n## Why\n\n%s\n\n## Benefit\n\n%s\n\n## Where\n\n", strings.TrimSpace(plan.Objective), what, why, benefit)
	seen := map[string]bool{}
	for _, path := range paths {
		if !seen[path] {
			fmt.Fprintf(&body, "- `%s`\n", path)
			seen[path] = true
		}
	}
	body.WriteString("\n## Acceptance criteria\n\n")
	for _, criterion := range plan.AcceptanceCriteria {
		fmt.Fprintf(&body, "- [ ] %s\n", strings.TrimSpace(criterion))
	}
	// A parsed plan retains its full document as Specification. Retain only its
	// optional detail section on subsequent renders rather than nesting documents.
	specification := strings.TrimSpace(plan.Specification)
	if title, sections, err := splitPlanDocument(specification); err == nil && title == strings.TrimSpace(plan.Objective) {
		specification = sections["specification"]
	}
	if specification != "" && specification != why {
		fmt.Fprintf(&body, "\n## Specification\n\n%s\n", specification)
	}
	body.WriteString("\n## Implementation\n\n")
	for _, unit := range plan.Units {
		if len(plan.Units) == 1 && unit.ID == "main" {
			fmt.Fprintf(&body, "%s\n", strings.TrimSpace(unit.Objective))
			continue
		}
		fmt.Fprintf(&body, "### %s\n\n%s\n\n", unit.ID, strings.TrimSpace(unit.Objective))
		fmt.Fprintf(&body, "Dependencies: %s\n\nAllowed paths: %s\n\n", jsonText(unit.Dependencies), jsonText(unit.AllowedPaths))
	}
	return strings.TrimSpace(body.String()) + "\n"
}

// ParsePlanDocument reads Markdown as text only. It never executes embedded
// code, interprets HTML, or trusts a hidden structured payload over visible prose.
func ParsePlanDocument(text string) (model.Plan, error) {
	objective, sections, err := splitPlanDocument(text)
	if err != nil {
		return model.Plan{}, err
	}
	for _, heading := range []string{"what", "why", "benefit", "where", "acceptance criteria", "implementation"} {
		if strings.TrimSpace(sections[heading]) == "" {
			return model.Plan{}, fmt.Errorf("plan requires nonempty %s section", heading)
		}
	}
	where, err := documentWhere(sections["where"])
	if err != nil {
		return model.Plan{}, err
	}
	plan := model.Plan{Objective: objective, What: sections["what"], Why: sections["why"], Benefit: sections["benefit"], Where: where, Specification: strings.TrimSpace(text)}
	plan.AcceptanceCriteria, err = documentList(sections["acceptance criteria"], true)
	if err == nil {
		plan.Units, err = parseDocumentUnits(sections["implementation"], where)
	}
	return plan, err
}

func documentWhere(text string) ([]string, error) {
	paths, err := documentList(text, false)
	if err != nil {
		return nil, fmt.Errorf("plan Where: %w", err)
	}
	seen := map[string]bool{}
	where := []string{}
	for _, entry := range paths {
		path := strings.TrimSpace(entry)
		if strings.HasPrefix(path, "`") && strings.HasSuffix(path, "`") {
			path = strings.Trim(path, "`")
		}
		if !safePlanDocumentPath(path) {
			return nil, fmt.Errorf("plan Where requires safe relative repository paths: %q", path)
		}
		if !seen[path] {
			where = append(where, path)
			seen[path] = true
		}
	}
	return where, nil
}

func safePlanDocumentPath(path string) bool {
	if path == "" || path == "." || !filepath.IsLocal(path) || strings.ContainsAny(path, "\\:\x00\r\n`<>") {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == ".." || part == ".git" || part == ".whirlwind.yaml" || part == ".whirlwind.yml" {
			return false
		}
	}
	return true
}

func documentList(text string, checklist bool) ([]string, error) {
	var values []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		match := planDocumentList.FindStringSubmatch(line)
		if match == nil {
			return nil, errors.New("each entry must be a Markdown list item")
		}
		value := strings.TrimSpace(match[1])
		if checklist {
			value = stripDocumentChecklist(value)
		}
		if value == "" {
			return nil, errors.New("list entries must not be empty")
		}
		values = append(values, value)
	}
	if len(values) == 0 {
		return nil, errors.New("at least one list entry required")
	}
	return values, nil
}

func stripDocumentChecklist(value string) string {
	if len(value) < 3 {
		return value
	}
	if value[0] == '[' && value[2] == ']' && strings.ContainsAny(value[1:2], " xX") {
		return strings.TrimSpace(value[3:])
	}
	return value
}
