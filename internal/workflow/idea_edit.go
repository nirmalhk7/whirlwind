package workflow

import (
	"context"
	"errors"
	"strings"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

// UpdateIdea retains generation ownership through the final conditional publication.
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
	if _, err := e.trustedProject(ctx, idea.ProjectID); err != nil {
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
	idea.NoveltyMethod, idea.NoveltyOutcome = "not_checked", "deferred"
	audit := model.Audit{Source: "local_operator", Action: "idea.update", Target: id, Version: expected, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	return e.Store.Write(ctx, []store.Change{{Kind: "ideas", ID: id, Value: idea, Expected: expected}, {Kind: "audit", ID: store.ID(), Value: audit}}, receipt)
}
