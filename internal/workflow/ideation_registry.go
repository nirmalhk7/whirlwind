package workflow

import (
	"context"
	"errors"
	"strings"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (e *Engine) ensureIdeationOperation(ctx context.Context, p model.Project, day string) (context.Context, func(error) error, error) {
	if operationID(ctx) != "" {
		return ctx, func(error) error { return nil }, nil
	}
	id := store.ID()
	op := model.Operation{Kind: "ideation", Subject: p.ID, Stage: "running", Owner: e.owner, StartedAt: time.Now().UTC().Format(time.RFC3339Nano), Input: map[string]string{"project_id": p.ID, "day": day}}
	if err := e.Store.Put(ctx, "operations", id, op, 0); err != nil {
		return ctx, nil, err
	}
	finish := func(failure error) error {
		return e.finishIdeationOperation(ctx, id, failure)
	}
	return WithOperation(ctx, id), finish, nil
}

func directionFromIdea(i model.Idea) model.Direction {
	return model.Direction{IdeaFields: &model.IdeaDraft{What: i.What, Why: i.Why, Where: i.Where, HowItHelps: i.HowItHelps}, ID: i.ID, ProjectID: i.ProjectID, Category: i.Direction, Title: i.Title, Description: i.HowItHelps, Problem: i.Why, Mechanism: i.Mechanism, Components: i.Components, Evidence: i.Evidence, RepositoryRevision: i.RepositoryRevision, Fingerprint: model.ProposalFingerprint(i.ProjectID, i.Why, i.Mechanism, i.Components), PredecessorID: i.PredecessorID, RevisitReason: i.RevisitReason}
}

func (e *Engine) acceptedRunDirections(ctx context.Context, run string) ([]model.Direction, error) {
	records, err := e.Store.List(ctx, "ideas")
	if err != nil {
		return nil, err
	}
	var accepted []model.Direction
	for _, record := range records {
		i, err := store.Decode[model.Idea](record)
		if err != nil {
			return nil, err
		}
		if i.RunID == run {
			accepted = append(accepted, directionFromIdea(i))
		}
	}
	return accepted, nil
}

func (e *Engine) reserveDirectionCandidates(ctx context.Context, run string, accepted []model.Direction) (map[string]string, error) {
	ids := map[string]string{}
	for _, direction := range missingDirectionCategories(accepted) {
		id, _, err := e.Store.ReserveIdeaCandidate(ctx, run, direction)
		if errors.Is(err, store.ErrConflict) {
			continue
		}
		if err != nil {
			return nil, err
		}
		ids[direction] = id
	}
	if len(ids) == 0 {
		return nil, store.ErrConflict
	}
	return ids, nil
}

func (e *Engine) publishDirectionCandidates(ctx context.Context, request directionGenerationRequest, directions []model.Direction) error {
	if operationID(ctx) != "" {
		if err := e.validateIdeationRunIdentity(ctx, request.project, request.snapshot.Commit); err != nil {
			return err
		}
	}
	accepted := map[string]model.Direction{}
	for _, direction := range directions {
		accepted[direction.Category] = direction
	}
	for category, id := range request.candidateIDs {
		d, ok := accepted[category]
		if !ok {
			if err := e.Store.CompleteIdeaCandidate(ctx, id, nil, "no valid candidate from bounded generation and content validation"); err != nil {
				return err
			}
			continue
		}
		// Supporting mechanism/components/evidence remain independent from the
		// four human fields. Source identities are always supervisor-owned.
		i := model.Idea{Title: d.Title, ID: id, ProjectID: request.project.ID, ProjectName: request.project.Name, RunID: request.runID, Direction: category, What: d.Title, Why: d.Problem, Where: strings.Join(d.Components, ", "), HowItHelps: d.Description, Mechanism: d.Mechanism, Components: d.Components, Evidence: d.Evidence, RepositoryRevision: d.RepositoryRevision, NoveltyMethod: "not_checked", NoveltyOutcome: "deferred", DecisionStatus: "draft", PredecessorID: d.PredecessorID}
		if d.IdeaFields != nil {
			i.What, i.Why, i.Where, i.HowItHelps = d.IdeaFields.What, d.IdeaFields.Why, d.IdeaFields.Where, d.IdeaFields.HowItHelps
		}
		i.RevisitReason = d.RevisitReason
		if err := e.Store.CompleteIdeaCandidate(ctx, id, &i, ""); err != nil {
			return err
		}
	}
	return nil
}

func exhaustedCandidateAllowance(err error) bool { return errors.Is(err, store.ErrConflict) }

func (e *Engine) finishIdeationOperation(ctx context.Context, id string, failure error) error {
	current, err := e.Store.Get(context.WithoutCancel(ctx), "operations", id)
	if err != nil {
		return err
	}
	saved, err := store.Decode[model.Operation](current)
	if err != nil {
		return err
	}
	if saved.Owner != e.owner || saved.Stage != "running" {
		return store.ErrConflict
	}
	op := saved
	op.Stage = "complete"
	if failure != nil {
		op.Stage, op.Error = "failed", failure.Error()
		var accepted int
		if err := e.Store.DB.QueryRowContext(context.WithoutCancel(ctx), "SELECT count(*) FROM ideas WHERE run_id=?", id).Scan(&accepted); err != nil {
			return err
		} else if accepted > 0 {
			op.Stage = "partial"
		}
		if errors.Is(failure, ErrHumanDecisionPending) {
			op.Stage = "paused"
		}
	}
	op.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return e.Store.Put(context.WithoutCancel(ctx), "operations", id, op, current.Version)
}
