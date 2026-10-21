package workflow

import (
	"context"
	"errors"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

type directionProposal struct {
	Directions []model.Direction
	Rejected   error
}

func (e *Engine) proposeDirections(ctx context.Context, project model.Project, scratch, prompt string, previous []model.Direction) (directionProposal, error) {
	out, err := e.agent(ctx, project, model.Task{ID: ideationGenerationID(ctx, project.ID), Issue: model.Issue{Title: "innovation", Body: jsonText(previous)}}, provider.Input{OutputContract: "directions", ValidateResult: directionOutputValidator(previous), Role: "ideate", CWD: scratch, Prompt: prompt, ReadOnly: true})
	if err != nil {
		var invalid *outputValidationError
		if errors.As(err, &invalid) {
			return directionProposal{Rejected: err}, nil
		}
		return directionProposal{}, err
	}
	directions, err := decodeDirectionBatch(out.Text, previous)
	return directionProposal{Directions: directions, Rejected: err}, nil
}

func directionOutputValidator(previous []model.Direction) func(string) error {
	return func(text string) error {
		_, err := decodeDirectionBatch(text, previous)
		return err
	}
}

func decodeDirectionBatch(text string, previous []model.Direction) ([]model.Direction, error) {
	var directions []model.Direction
	if err := provider.JSON(text, &directions); err != nil {
		return nil, err
	}
	return directions, validateDirections(directions, previous)
}

func ideationGenerationID(ctx context.Context, project string) string {
	return ideationPrefix + project + ":" + operationID(ctx)
}
