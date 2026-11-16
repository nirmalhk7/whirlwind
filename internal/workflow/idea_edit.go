package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

type IdeaSimilarityError struct{ Reason string }

func (e *IdeaSimilarityError) Error() string { return "similar idea rejected: " + e.Reason }

// UpdateIdea retains generation ownership through novelty review and the final
// conditional publication. Agent calls never run inside a SQLite transaction.
func (e *Engine) UpdateIdea(ctx context.Context, id string, expected int, draft model.IdeaDraft, receipt *store.Receipt) error {
	record, err := e.Store.Get(ctx, "ideas", id)
	if err != nil {
		return err
	}
	idea, err := store.Decode[model.Idea](record)
	if err != nil {
		return err
	}
	if record.Version != expected || idea.DecisionStatus != "draft" || idea.ArchivedAt != "" {
		return store.ErrConflict
	}
	idea.What, idea.Why, idea.Where, idea.HowItHelps = strings.TrimSpace(draft.What), strings.TrimSpace(draft.Why), strings.TrimSpace(draft.Where), strings.TrimSpace(draft.HowItHelps)
	for _, field := range []string{idea.What, idea.Why, idea.Where, idea.HowItHelps} {
		if field == "" {
			return errors.New("each idea field must be nonempty")
		}
	}
	project, err := e.trustedProject(ctx, idea.ProjectID)
	if err != nil {
		return err
	}
	key := "generation:" + idea.ProjectID
	lease, err := e.Store.Claim(ctx, key, e.owner, time.Minute)
	if err != nil {
		return err
	}
	defer func() { _ = e.Store.Release(context.Background(), key, e.owner, lease.Fence) }()
	ctx, cancel := context.WithCancel(store.WithLeaseFence(ctx, key, lease))
	defer cancel()
	e.renewLease(ctx, cancel, key, lease.Fence, nil)
	ctx = withIdeationHistoryBudget(ctx, e.settings(ctx).Ideation)
	_, previous, snapshot, _, err := e.prepareDiscovery(WithFreshDiscovery(ctx, true), project)
	if err != nil {
		return err
	}
	history, err := e.ideaEditHistory(ctx, idea, previous)
	if err != nil {
		return err
	}
	candidate := directionFromIdea(idea)
	candidate.Description = idea.HowItHelps + "\nAffected area: " + idea.Where
	request := directionGenerationRequest{project: project, snapshot: snapshot, runID: fmt.Sprintf("%s-edit-%s-v%d", idea.RunID, id, expected), budgetTaskID: ideationPrefix + idea.ProjectID + ":edit:" + id + fmt.Sprint(":", expected)}
	unique, reason, _, err := e.reviewDirectionNovelty(ctx, request, []model.Direction{candidate}, history)
	if err != nil {
		return err
	}
	if !unique {
		if strings.TrimSpace(reason) == "" {
			reason = "retained history or current implementation already covers this outcome"
		}
		return &IdeaSimilarityError{Reason: reason}
	}
	idea.NoveltyMethod, idea.NoveltyOutcome = "legacy_llm", "accepted"
	audit := model.Audit{Source: "local_operator", Action: "idea.update", Target: id, Version: expected, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	return e.Store.Write(ctx, []store.Change{{Kind: "ideas", ID: id, Value: idea, Expected: expected}, {Kind: "audit", ID: store.ID(), Value: audit}}, receipt)
}

func (e *Engine) ideaEditHistory(ctx context.Context, idea model.Idea, previous []model.Direction) ([]model.Direction, error) {
	// Exclude the edited idea before applying the retrieval limit. Archival
	// does not exclude history from novelty checks.
	rows, err := e.Store.DB.QueryContext(ctx, `SELECT data FROM resources WHERE kind='ideas' AND json_extract(data,'$.project_id')=? AND id<>? ORDER BY updated_at DESC,id LIMIT 12`, idea.ProjectID, idea.ID)
	if err != nil {
		return nil, err
	}
	var history []model.Direction
	seen := map[string]bool{idea.ID: true}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			_ = rows.Close()
			return nil, err
		}
		var i model.Idea
		if err := json.Unmarshal(raw, &i); err != nil {
			_ = rows.Close()
			return nil, err
		}
		seen[i.ID] = true
		history = append(history, directionFromIdea(i))
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, p := range previous {
		if !seen[p.ID] {
			history = append(history, p)
			seen[p.ID] = true
		}
	}
	return boundProposalHistory(history, e.proposalHistoryRequestLimit(ctx), 12), nil
}
