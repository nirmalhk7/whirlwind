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
	ctx, release, err := e.projectRunnerContext(ctx, p.ID)
	if err != nil {
		return err
	}
	defer release()
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
	prompt, previous, snapshot, unchanged, err := e.prepareDiscovery(ctx, p)
	if err != nil {
		return err
	}
	if unchanged {
		return e.Store.SaveDirections(ctx, p.ID, day, nil)
	}
	directions, err := e.generateDirections(ctx, p, prompt, previous, snapshot)
	if err != nil {
		return err
	}
	for n := range directions {
		d := &directions[n]
		d.Rank = n + 1
		d.ID = store.ID()
		d.ProjectID = p.ID
		d.Day = day
	}
	if err = e.Store.SaveDirections(ctx, p.ID, day, directions); err != nil {
		return err
	}
	if len(directions) > 0 {
		if err = e.Store.Notify(ctx, "", "ideas_ready", fmt.Sprintf("%s: %d evidence-backed ideas await your selection", p.Name, len(directions))); err != nil {
			return err
		}
	}
	return e.saveDiscoverySnapshot(ctx, p.ID, snapshot)
}

// IdeateWhenAvailable queues a manual run behind active generation for this project.
func (e *Engine) IdeateWhenAvailable(ctx context.Context, p model.Project, day string) error {
	ctx, release, err := e.projectRunnerContext(ctx, p.ID)
	if err != nil {
		return err
	}
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
func (e *Engine) generateDirections(ctx context.Context, p model.Project, prompt string, previous []model.Direction, snapshot discoverySnapshot) ([]model.Direction, error) {
	scratch := filepath.Join(e.Root, "ideation", p.ID)
	if err := os.MkdirAll(scratch, 0700); err != nil {
		return nil, err
	}
	var rejected error
	snapshotID := snapshot.Fingerprint
	if snapshotID == "" {
		snapshotID = model.Fingerprint([]byte(prompt))
	}
	budgetTaskID := ideationPrefix + p.ID + ":" + snapshotID[:12]
	if operation := operationID(ctx); operation != "" {
		budgetTaskID = ideationPrefix + p.ID + ":" + operation
	}
	for attempt := 0; attempt < 3; attempt++ {
		input := prompt
		if rejected != nil {
			input += "\nPrevious batch rejected: " + rejected.Error() + "\nReturn up to four useful evidence-backed candidates, or [] when none qualify."
		}
		out, err := e.agent(ctx, p, model.Task{ID: budgetTaskID, Issue: model.Issue{Title: "innovation"}}, provider.Input{Role: "ideate", Purpose: "generation", CWD: scratch, Prompt: input, ReadOnly: true})
		if err != nil {
			return nil, err
		}
		var directions []model.Direction
		if err = provider.JSON(out.Text, &directions); err != nil {
			rejected = err
			continue
		}
		if err = validateCandidateEvidence(directions, snapshot); err != nil {
			rejected = err
			continue
		}
		unchangedHistory := unchangedCandidateEvidence(previous, snapshot.Evidence)
		if err = validateDirections(directions, unchangedHistory, snapshot.MaxCandidates); err != nil {
			rejected = err
			continue
		}
		if len(directions) == 0 || len(unchangedHistory) == 0 {
			return directions, nil
		}
		unique, reason, err := e.reviewDirectionNovelty(ctx, p, scratch, directions, unchangedHistory, budgetTaskID)
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
func (e *Engine) reviewDirectionNovelty(ctx context.Context, p model.Project, scratch string, directions, previous []model.Direction, budgetTaskID string) (bool, string, error) {
	prompt, err := agents.Render("ideate_novelty.md", map[string]string{"Candidates": boundedSources(jsonText(directions)), "Previous": boundedSources(jsonText(previous))})
	if err != nil {
		return false, "", err
	}
	out, err := e.agent(ctx, p, model.Task{ID: budgetTaskID, Issue: model.Issue{Title: "idea novelty review"}}, provider.Input{Role: "ideate", Purpose: "novelty:" + store.ID(), CWD: scratch, Prompt: prompt, ReadOnly: true})
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
