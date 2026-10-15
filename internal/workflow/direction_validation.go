package workflow

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"whirlwind/internal/model"
	"whirlwind/prompts/agents"
)

func validateDirections(directions, previous []model.Direction) error {
	if len(directions) != 4 {
		return errors.New("ideation must produce four directions")
	}
	titles, descriptions := map[string]bool{}, map[string]bool{}
	for _, direction := range previous {
		titles[directionKey(direction.Title)] = true
		descriptions[directionKey(direction.Description)] = true
	}
	categories := map[string]bool{}
	for _, direction := range directions {
		if err := validateDirectionContent(direction); err != nil {
			return err
		}
		if categories[direction.Category] {
			return errors.New("ideation must produce one idea per category: expand, optimize, harden, simplify")
		}
		categories[direction.Category] = true
		title, description := directionKey(direction.Title), directionKey(direction.Description)
		if title == "" || description == "" || titles[title] || descriptions[description] {
			return errors.New("duplicate or incomplete direction; retry ideation")
		}
		titles[title], descriptions[description] = true, true
	}
	return nil
}

func validateDirectionContent(direction model.Direction) error {
	if len(strings.Fields(direction.Description)) > agents.MaxIdeaDescriptionWords {
		return fmt.Errorf("idea description exceeds %d words", agents.MaxIdeaDescriptionWords)
	}
	switch direction.Category {
	case "expand", "optimize", "harden", "simplify":
	default:
		return errors.New("each idea needs category expand, optimize, harden, or simplify")
	}
	return nil
}

// directionKey ignores case, punctuation, and whitespace when checking repeated wording.
func directionKey(value string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(value), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }), " ")
}
