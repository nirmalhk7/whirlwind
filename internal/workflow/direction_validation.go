package workflow

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"whirlwind/internal/model"
	"whirlwind/prompts/agents"
)

var directionCategories = []string{"expand", "optimize", "harden", "simplify"}

func missingDirectionCategories(directions []model.Direction) []string {
	present := map[string]bool{}
	for _, direction := range directions {
		present[direction.Category] = true
	}
	missing := make([]string, 0, len(directionCategories))
	for _, category := range directionCategories {
		if !present[category] {
			missing = append(missing, category)
		}
	}
	return missing
}

func validateCompleteDirections(directions []model.Direction) error {
	if err := validateDirections(directions, nil, 4); err != nil {
		return err
	}
	if len(directions) != len(directionCategories) || len(missingDirectionCategories(directions)) != 0 {
		return fmt.Errorf("ideation requires exactly one candidate in each category; missing: %s", strings.Join(missingDirectionCategories(directions), ", "))
	}
	return nil
}

func validateDirections(directions, previous []model.Direction, maximum ...int) error {
	limit := 4
	if len(maximum) > 0 && maximum[0] > 0 {
		limit = maximum[0]
	}
	if len(directions) > limit {
		return fmt.Errorf("ideation must produce at most %d directions", limit)
	}
	titles, descriptions, categories := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, direction := range previous {
		titles[directionKey(direction.Title)] = true
		descriptions[directionKey(direction.Description)] = true
	}
	for _, direction := range directions {
		if err := validateDirectionContent(direction); err != nil {
			return err
		}
		title, description := directionKey(direction.Title), directionKey(direction.Description)
		if title == "" || description == "" || titles[title] || descriptions[description] || categories[direction.Category] {
			return errors.New("duplicate or incomplete direction; retry ideation")
		}
		titles[title], descriptions[description] = true, true
		categories[direction.Category] = true
	}
	return nil
}

func validateDirectionContent(direction model.Direction) error {
	if len(strings.Fields(direction.Description)) > agents.MaxIdeaDescriptionWords {
		return fmt.Errorf("idea description exceeds %d words", agents.MaxIdeaDescriptionWords)
	}
	for _, category := range directionCategories {
		if direction.Category == category {
			return nil
		}
	}
	{
		return errors.New("each idea needs category expand, optimize, harden, or simplify")
	}
}

// directionKey ignores case, punctuation, and whitespace when checking repeated wording.
func directionKey(value string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(value), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }), " ")
}
