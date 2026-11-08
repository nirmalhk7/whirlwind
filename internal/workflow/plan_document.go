package workflow

import (
	"encoding/json"
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
	writePlanList(&body, "Scope", plan.Scope, "Pending human review: implementation scope is not documented.")
	writePlanList(&body, "Exclusions", plan.Exclusions, "Pending human review: exclusions are not documented.")
	writePlanList(&body, "Implementation steps", plan.Steps, "Pending human review: implementation steps are not documented.")
	body.WriteString("\n## Requirements\n\n")
	for _, requirement := range plan.Requirements {
		encoded, _ := json.Marshal(requirement)
		fmt.Fprintf(&body, "- %s: %s — Evidence: %s <!-- %s -->\n", requirement.ID, requirement.Outcome, requirement.EvidenceMethod, encoded)
	}
	body.WriteString("\n## Checks\n\n")
	for _, check := range plan.Checks {
		encoded, _ := json.Marshal(check)
		name := strings.TrimSpace(check.Name)
		if name == "" {
			name = "Approved check"
		}
		fmt.Fprintf(&body, "- %s: `%s` <!-- %s -->\n", name, strings.Join(check.Command, " "), encoded)
	}
	body.WriteString("\n## Repository test commands\n\n")
	writePlanCommands(&body, plan.RepositoryTestCommands)
	fmt.Fprintf(&body, "\n## Repository test command inventory\n\n%s\n", inventoryText(plan.RepositoryTestInventory))
	body.WriteString("\n## Repository build commands\n\n")
	writePlanCommands(&body, plan.RepositoryBuildCommands)
	fmt.Fprintf(&body, "\n## Repository build command inventory\n\n%s\n", inventoryText(plan.RepositoryBuildInventory))
	// A parsed plan retains its full document as Specification. Retain only its
	// optional detail section on subsequent renders rather than nesting documents.
	specification := strings.TrimSpace(plan.Specification)
	if title, sections, err := splitPlanDocument(specification); err == nil && title == strings.TrimSpace(plan.Objective) {
		specification = sections["specification"]
	}
	if specification != "" {
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
	for _, heading := range []string{"what", "why", "benefit", "where", "acceptance criteria", "implementation", "requirements", "checks", "repository test commands", "repository build commands"} {
		if strings.TrimSpace(sections[heading]) == "" && !optionalPlanSection(heading) {
			return model.Plan{}, fmt.Errorf("plan requires nonempty %s section", heading)
		}
	}
	where, err := documentWhere(sections["where"])
	if err != nil {
		return model.Plan{}, err
	}
	plan := model.Plan{Objective: objective, What: sections["what"], Why: sections["why"], Benefit: sections["benefit"], Where: where, Specification: strings.TrimSpace(text)}
	plan.Scope, err = parsePlanDetailList(sections["scope"])
	if err == nil {
		plan.Exclusions, err = parsePlanDetailList(sections["exclusions"])
	}
	if err == nil {
		plan.Steps, err = parsePlanDetailList(sections["implementation steps"])
	}
	plan.RepositoryTestInventory = inventoryValue(sections["repository test command inventory"])
	plan.RepositoryBuildInventory = inventoryValue(sections["repository build command inventory"])
	if err == nil {
		plan.AcceptanceCriteria, err = documentList(sections["acceptance criteria"], true)
	}
	if err == nil {
		plan.Units, err = parseDocumentUnits(sections["implementation"], where)
	}
	if err == nil {
		plan.Requirements, err = parseDocumentJSONList[model.Requirement](sections["requirements"])
	}
	if err == nil {
		plan.Checks, err = parseDocumentJSONList[model.Check](sections["checks"])
	}
	if err == nil {
		plan.RepositoryTestCommands, err = parsePlanCommands(sections["repository test commands"])
	}
	if err == nil {
		plan.RepositoryBuildCommands, err = parsePlanCommands(sections["repository build commands"])
	}
	return plan, err
}

func writePlanList(body *strings.Builder, heading string, values []string, pending string) {
	fmt.Fprintf(body, "\n## %s\n\n", heading)
	if len(values) == 0 {
		body.WriteString(pending + "\n")
		return
	}
	for _, value := range values {
		fmt.Fprintf(body, "- %s\n", strings.TrimSpace(value))
	}
}

func parsePlanDetailList(section string) ([]string, error) {
	if strings.TrimSpace(section) == "" || isPendingPlanSection(section) {
		return nil, nil
	}
	return documentList(section, false)
}

func isPendingPlanSection(section string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(section)), "pending human review:")
}

func inventoryText(value string) string {
	if strings.TrimSpace(value) == "" {
		return "Pending human review: command inventory is not documented."
	}
	return strings.TrimSpace(value)
}

func inventoryValue(section string) string {
	value := strings.TrimSpace(section)
	if isPendingPlanSection(value) {
		return ""
	}
	return value
}

func writePlanCommands(body *strings.Builder, commands [][]string) {
	for _, command := range commands {
		encoded, _ := json.Marshal(command)
		fmt.Fprintf(body, "- `%s` <!-- %s -->\n", strings.Join(command, " "), encoded)
	}
}

func parseDocumentJSONList[T any](text string) ([]T, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	lines, err := documentList(text, false)
	if err != nil {
		return nil, err
	}
	values := make([]T, 0, len(lines))
	for _, line := range lines {
		line = planMetadata(line)
		line = strings.Trim(line, "`")
		var value T
		if err := json.Unmarshal([]byte(line), &value); err != nil {
			return nil, fmt.Errorf("invalid plan entry: %w", err)
		}
		values = append(values, value)
	}
	return values, nil
}

func planMetadata(line string) string {
	if start := strings.LastIndex(line, "<!--"); start >= 0 {
		if end := strings.Index(line[start:], "-->"); end > 0 {
			return strings.TrimSpace(line[start+4 : start+end])
		}
	}
	return line
}

func parsePlanCommands(text string) ([][]string, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	lines, err := documentList(text, false)
	if err != nil {
		return nil, err
	}
	commands := make([][]string, 0, len(lines))
	for _, line := range lines {
		line = planMetadata(line)
		line = strings.Trim(line, "`")
		var command []string
		if err := json.Unmarshal([]byte(line), &command); err != nil {
			command = strings.Fields(line)
		}
		if len(command) == 0 {
			return nil, errors.New("plan commands must be nonempty")
		}
		commands = append(commands, command)
	}
	return commands, nil
}

func optionalPlanSection(heading string) bool {
	return heading == "requirements" || heading == "checks" || heading == "repository test commands" || heading == "repository build commands"
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
