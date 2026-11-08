package verification

import (
	"path/filepath"
	"regexp"
	"strings"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
)

func isQltySmellsCheck(check model.Check) bool {
	return len(check.Command) > 1 && filepath.Base(check.Command[0]) == "qlty" && check.Command[1] == "smells"
}

// Select keeps baseline checks, adds checks for matching path, code-type, or API rules, and selects the highest matched risk.
func Select(p model.Policy, files []string, diff ...string) (string, []model.Check) {
	symbols := changedAPISymbols(diff)
	extensions := make([]string, len(files))
	for n, file := range files {
		extensions[n] = filepath.Ext(file)
	}
	levels := map[string]int{"low": 0, "medium": 1, "high": 2, "critical": 3}
	risk := "low"
	matchedAny := false
	checks := append([]model.Check{}, p.Checks...)
	seen := map[string]bool{}
	for _, c := range checks {
		seen[c.Name] = true
	}
	for _, rule := range p.RiskRules {
		if !matchesAny(rule.Paths, files, policy.Match) &&
			!matchesAny(rule.APISymbols, symbols, policy.Match) &&
			!matchesAny(rule.CodeTypes, extensions, strings.EqualFold) {
			continue
		}
		matchedAny = true
		if levels[rule.Level] > levels[risk] {
			risk = rule.Level
		}
		for _, c := range rule.Checks {
			if !seen[c.Name] {
				checks = append(checks, c)
				seen[c.Name] = true
			}
		}
	}
	if !matchedAny {
		risk = "medium"
	}
	return risk, checks
}

// matchesAny reports whether any configured selector matches any changed value.
func matchesAny(patterns, values []string, match func(string, string) bool) bool {
	for _, pattern := range patterns {
		for _, value := range values {
			if match(pattern, value) {
				return true
			}
		}
	}
	return false
}

var apiPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?m)^[-+]\s*func\s+(?:\([^)]*\)\s*)?([A-Z][A-Za-z0-9_]*)`),
	regexp.MustCompile(`(?m)^[-+]\s*type\s+([A-Z][A-Za-z0-9_]*)`),
	regexp.MustCompile(`(?m)^[-+]\s*export\s+(?:default\s+)?(?:function|class|type|interface|const)\s+([A-Za-z_][A-Za-z0-9_]*)`),
	regexp.MustCompile(`(?m)^[-+].*HandleFunc\("([A-Z]+ /[^" ]*)"`),
}

func changedAPISymbols(diff []string) []string {
	symbols := []string{}
	for _, patch := range diff {
		for _, pattern := range apiPatterns {
			for _, match := range pattern.FindAllStringSubmatch(patch, -1) {
				symbols = append(symbols, match[1])
			}
		}
	}
	return symbols
}
