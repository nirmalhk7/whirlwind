package workflow

import (
	"encoding/json"
	"errors"
	"strings"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

// The public generation contract has explicit human fields. Direction remains
// an internal compatibility carrier until its remaining consumers are retired.
func decodeGeneratedIdeas(text string) ([]model.Direction, error) {
	var entries []json.RawMessage
	if err := provider.JSON(text, &entries); err != nil {
		return nil, err
	}
	directions := make([]model.Direction, 0, len(entries))
	for _, raw := range entries {
		supporting, err := decodeGeneratedIdea(raw)
		if err != nil {
			return nil, err
		}
		directions = append(directions, supporting)
	}
	return directions, nil
}

func decodeGeneratedIdea(raw json.RawMessage) (model.Direction, error) {
	var supporting model.Direction
	if err := json.Unmarshal(raw, &supporting); err != nil {
		return model.Direction{}, err
	}
	var explicit struct {
		model.IdeaDraft
		Direction string `json:"direction"`
	}
	if err := json.Unmarshal(raw, &explicit); err != nil {
		return model.Direction{}, err
	}
	supporting.IdeaFields = nil
	if explicit.Direction != "" || explicit.What != "" || explicit.Why != "" || explicit.Where != "" || explicit.HowItHelps != "" {
		fields := model.IdeaDraft{What: strings.TrimSpace(explicit.What), Why: strings.TrimSpace(explicit.Why), Where: strings.TrimSpace(explicit.Where), HowItHelps: strings.TrimSpace(explicit.HowItHelps)}
		if strings.TrimSpace(explicit.Direction) == "" || fields.What == "" || fields.Why == "" || fields.Where == "" || fields.HowItHelps == "" {
			return model.Direction{}, errors.New("each generated idea requires direction, what, why, where, and how_it_helps")
		}
		if generatedIdeaFieldsConflict(supporting, explicit.Direction, fields) {
			return model.Direction{}, errors.New("generated idea has conflicting human fields")
		}
		supporting.Category, supporting.Title, supporting.Problem, supporting.Description = explicit.Direction, fields.What, fields.Why, fields.HowItHelps
		supporting.IdeaFields = &fields
	}
	return supporting, nil
}

func generatedIdeaFieldsConflict(supporting model.Direction, direction string, fields model.IdeaDraft) bool {
	return supporting.Category != "" && supporting.Category != direction || supporting.Title != "" && supporting.Title != fields.What || supporting.Problem != "" && supporting.Problem != fields.Why || supporting.Description != "" && supporting.Description != fields.HowItHelps
}
