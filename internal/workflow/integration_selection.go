package workflow

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/prompts/agents"
)

var integrationAnnotation = regexp.MustCompile(`(?:^|[^[:alnum:]_@])@([[:alpha:]][[:alnum:]_-]*)\b`)

func parseIntegrationAnnotations(prompt string) []string {
	seen := map[string]bool{}
	annotations := []string{}
	for _, match := range integrationAnnotation.FindAllStringSubmatch(prompt, -1) {
		name := strings.TrimSpace(match[1])
		key := integrationNameKey(name)
		if key != "" && !seen[key] {
			seen[key] = true
			annotations = append(annotations, name)
		}
	}
	slices.SortFunc(annotations, func(a, b string) int { return strings.Compare(integrationNameKey(a), integrationNameKey(b)) })
	return annotations
}

func (e *Engine) promptSelectedIntegrationSources(ctx context.Context, role string) ([]model.IntegrationSource, error) {
	name := role + ".md"
	readPrompt := e.integrationPromptSource
	if readPrompt == nil {
		readPrompt = agents.Source
	}
	trustedPrompt, err := readPrompt(name)
	if err != nil {
		return nil, err
	}
	annotations := parseIntegrationAnnotations(trustedPrompt)
	if len(annotations) == 0 {
		return nil, nil
	}
	allowed := e.WorkflowConfiguration(ctx).Integrations.Sources
	selected := make([]model.IntegrationSource, 0, len(annotations))
	for _, annotation := range annotations {
		matches := []model.IntegrationSource{}
		for _, source := range allowed {
			if e.integrationSourceMatches(ctx, annotation, source) {
				matches = append(matches, source)
			}
		}
		switch len(matches) {
		case 0:
			return nil, fmt.Errorf("prompt integration @%s is not in the authorized integration source list", annotation)
		case 1:
			selected = append(selected, matches[0])
		default:
			return nil, fmt.Errorf("prompt integration @%s matches multiple authorized connections; select one unique service reference", annotation)
		}
	}
	selected, _, err = selectedIntegrationScopes(selected)
	return selected, err
}

func (e *Engine) integrationSourceMatches(ctx context.Context, annotation string, source model.IntegrationSource) bool {
	key := integrationNameKey(annotation)
	if key == integrationNameKey(source.Provider) || key == integrationNameKey(strings.TrimPrefix(source.Ref, "mcp://")) {
		return true
	}
	record, err := e.Store.Get(ctx, "providers", source.Provider)
	if err != nil {
		return false
	}
	provider, err := store.Decode[model.Provider](record)
	if err != nil {
		return false
	}
	return key != "" && key == integrationNameKey(provider.Name)
}

func integrationNameKey(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.TrimPrefix(name, "mcp://")
	var key strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			key.WriteRune(r)
		}
	}
	return key.String()
}
