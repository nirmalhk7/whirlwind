package workflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/prompts/agents"
)

const ideationPrefix = "ideation:"

// Ideate retains a generation lease while preparing, validating, and saving four directions.
func (e *Engine) Ideate(ctx context.Context, p model.Project, day string) error {
	ctx, release := e.runnerContext(ctx)
	defer release()
	if operationID(ctx) == "" {
		ctx = WithOperation(ctx, p.ID+":"+day)
	}
	key := "generation:" + p.ID
	lease, err := e.Store.Claim(ctx, key, e.owner, time.Minute)
	if err != nil {
		return err
	}
	defer func() { _ = e.Store.Release(context.Background(), key, e.owner, lease.Fence) }()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Recheck after acquiring the lease: a scheduled project may have been unregistered.
	if _, err := e.Project(ctx, p.ID); err != nil {
		return err
	}
	e.renewLease(ctx, cancel, key, lease.Fence, nil)
	prompt, previous, err := e.ideationPrompt(ctx, p)
	if err != nil {
		return err
	}
	directions, err := e.generateDirections(ctx, p, prompt, previous)
	if err != nil {
		return err
	}
	for n := range directions {
		d := &directions[n]
		d.ID = store.ID()
		d.ProjectID = p.ID
		d.Day = day
	}
	return e.Store.SaveDirections(ctx, p.ID, day, directions)
}

// IdeateWhenAvailable queues a manual run behind active generation for this project.
func (e *Engine) IdeateWhenAvailable(ctx context.Context, p model.Project, day string) error {
	ctx, release := e.runnerContext(ctx)
	defer release()
	for {
		err := e.Ideate(ctx, p, day)
		if !errors.Is(err, store.ErrLeased) {
			return err
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (e *Engine) ideationPrompt(ctx context.Context, p model.Project) (string, []model.Direction, error) {
	index, err := e.index(ctx, p)
	if err != nil {
		return "", nil, err
	}
	sources, err := e.GitHub.Sources(ctx, p.Repo)
	if err != nil {
		return "", nil, err
	}
	_ = e.Store.Put(ctx, "source_cache", store.ID(), map[string]any{"project": p.ID, "sources": sources}, 0)
	previous, err := e.previousDirections(ctx, p.ID)
	if err != nil {
		return "", nil, err
	}
	summary := []map[string]any{}
	for _, file := range index.Files {
		summary = append(summary, map[string]any{"path": file.Path, "symbols": file.Symbols, "todos": file.TODOs})
	}
	prompt, err := agents.Render("ideate.md", map[string]string{"Index": jsonText(summary), "Sources": jsonText(sources), "Previous": jsonText(previous)})
	return prompt, previous, err
}
func (e *Engine) previousDirections(ctx context.Context, project string) ([]model.Direction, error) {
	records, err := e.Store.List(ctx, "directions")
	if err != nil {
		return nil, err
	}
	previous := []model.Direction{}
	for _, record := range records {
		value, err := store.Decode[model.Direction](record)
		if err != nil {
			return nil, err
		}
		if value.ProjectID == project {
			previous = append(previous, value)
		}
	}
	return previous, nil
}
func (e *Engine) generateDirections(ctx context.Context, p model.Project, prompt string, previous []model.Direction) ([]model.Direction, error) {
	scratch := filepath.Join(e.Root, "ideation", p.ID)
	if err := os.MkdirAll(scratch, 0700); err != nil {
		return nil, err
	}
	var rejected error
	for attempt := 0; attempt < 3; attempt++ {
		input := prompt
		if rejected != nil {
			input += "\nPrevious batch rejected: " + rejected.Error() + "\nGenerate a replacement batch of four substantively different ideas."
		}
		proposal, err := e.proposeDirections(ctx, p, scratch, input, previous)
		if err != nil {
			return nil, err
		}
		if proposal.Rejected != nil {
			rejected = proposal.Rejected
			continue
		}
		directions := proposal.Directions
		unique, reason, err := e.reviewDirectionNovelty(ctx, p, scratch, directions, previous)
		if err != nil {
			return nil, err
		}
		if unique {
			return directions, nil
		}
		rejected = fmt.Errorf("ideas overlap in substance: %s", reason)
	}
	return nil, fmt.Errorf("ideation rejected after three attempts: %w", rejected)
}

// reviewDirectionNovelty checks meaning as well as wording, including overlap across categories.
func (e *Engine) reviewDirectionNovelty(ctx context.Context, p model.Project, scratch string, directions, previous []model.Direction) (bool, string, error) {
	prompt, err := agents.Render("ideate_novelty.md", map[string]string{"Candidates": jsonText(directions), "Previous": jsonText(previous)})
	if err != nil {
		return false, "", err
	}
	out, err := e.agent(ctx, p, model.Task{ID: ideationGenerationID(ctx, p.ID) + ":novelty", Issue: model.Issue{Title: "idea novelty review"}}, provider.Input{OutputContract: "novelty", Role: "ideate", CWD: scratch, Prompt: prompt, ReadOnly: true})
	if err != nil {
		return false, "", err
	}
	var verdict struct {
		Unique *bool  `json:"unique"`
		Reason string `json:"reason"`
	}
	if err = provider.JSON(out.Text, &verdict); err != nil {
		return false, "", err
	}
	if verdict.Unique == nil {
		return false, "", errors.New("novelty review missing unique verdict")
	}
	return *verdict.Unique, verdict.Reason, nil
}
