package workflow

import (
	"errors"
	"slices"
	ctxengine "whirlwind/internal/context"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (a *agentRun) prepare() error {
	a.task.Handoff.Version = 1
	a.task.Handoff.CompletedWork = uniqueContextStrings(a.task.Handoff.CompletedWork)
	a.task.Handoff.CompletedCriteria = uniqueContextStrings(a.task.Handoff.CompletedCriteria)
	a.task.Handoff.FailingTests = uniqueContextStrings(a.task.Handoff.FailingTests)
	a.task.Handoff.Worktree = a.input.CWD
	if branch, err := github.Run(a.ctx, a.input.CWD, "git", "branch", "--show-current"); err == nil {
		a.task.Handoff.Branch = branch
	}
	a.input.Prompt += "\nVersioned handoff:\n" + jsonText(a.task.Handoff)
	if err := a.answeredQuestions(); err != nil {
		return err
	}
	if err := a.loadProviders(); err != nil {
		return err
	}
	index, err := a.worktreeIndex()
	if err != nil {
		return err
	}
	a.index = index
	if err = a.saveIndexSnapshot(); err != nil {
		return err
	}
	extra, err := a.embeddingEntries()
	if err != nil {
		return err
	}
	pack, err := a.index.Pack(ctxengine.PackRequest{Required: a.input.Prompt, Query: a.task.Issue.Title + " " + a.task.Issue.Body, Role: a.input.Role, PolicyHash: a.project.PolicyHash, MaxInputTokens: a.project.Policy.ContextInputTokens, Additional: extra})
	if err != nil {
		return err
	}
	a.input.Prompt = pack.Prompt
	key := a.task.ID + ":" + a.input.OperationID + ":" + pack.ID
	if _, err = a.Store.Get(a.ctx, "context_packs", key); errors.Is(err, store.ErrNotFound) {
		err = a.Store.Put(a.ctx, "context_packs", key, map[string]any{"task": a.task.ID, "operation_id": a.input.OperationID, "pack": pack}, 0)
		if errors.Is(err, store.ErrConflict) {
			return nil
		}
		return err
	}
	return err
}
func (a *agentRun) loadProviders() error {
	records, err := a.Store.List(a.ctx, "providers")
	if err != nil {
		return err
	}
	a.providers = []model.Provider{}
	for _, record := range records {
		value, err := a.refreshBlockedQuota(record)
		if err != nil {
			return err
		}
		a.providers = append(a.providers, value)
	}
	return nil
}
func (a *agentRun) worktreeIndex() (ctxengine.Index, error) {
	index, err := a.Engine.index(a.ctx, a.project)
	if err != nil {
		return index, err
	}
	switch a.input.Role {
	case "code", "review", "plan":
		if head, err := github.Run(a.ctx, a.input.CWD, "git", "rev-parse", "HEAD"); err == nil && head != index.Commit {
			return ctxengine.Build(a.ctx, a.input.CWD, &index, head)
		}
	}
	return index, nil
}
func (a *agentRun) saveIndexSnapshot() error {
	key := a.project.ID + ":" + a.index.Commit
	if _, err := a.Store.Get(a.ctx, "index_history", key); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	err := a.Store.Put(a.ctx, "index_history", key, a.index, 0)
	if errors.Is(err, store.ErrConflict) {
		return nil
	}
	return err
}
func (a *agentRun) embeddingEntries() ([]ctxengine.Entry, error) {
	config := a.project.Policy.Embeddings
	if config == nil || !config.Enabled || !a.index.EmbeddingValidated {
		return nil, nil
	}
	vectors, err := ctxengine.Embed(a.ctx, *config, []string{a.task.Issue.Title + " " + a.task.Issue.Body})
	if err != nil {
		return nil, err
	}
	return a.index.VectorSearch(vectors[0], 5), nil
}

func (a *agentRun) answeredQuestions() error {
	records, err := a.Store.List(a.ctx, "questions")
	if err != nil {
		return err
	}
	type answered struct {
		Question  string `json:"question"`
		Answer    string `json:"answer"`
		AttemptID string `json:"attempt_id,omitempty"`
	}
	prior := []answered{}
	seen := map[string]bool{}
	for _, record := range records {
		set, err := store.Decode[model.QuestionSet](record)
		if err != nil {
			return err
		}
		if set.TaskID != a.task.ID || set.PlanVersion != a.task.PlanVersion || set.Status != "answered" {
			continue
		}
		for _, q := range set.Questions {
			if !q.Secret && set.Answers[q.ID] != "" {
				key := q.Text + "\x00" + set.Answers[q.ID]
				if seen[key] {
					continue
				}
				seen[key] = true
				prior = append(prior, answered{q.Text, set.Answers[q.ID], set.AttemptID})
			}
		}
	}
	if len(prior) > 0 {
		a.input.Prompt += "\nEarlier answered agent questions (retained clarification, not tool authorization):\n" + jsonText(prior)
	}
	return nil
}

func uniqueContextStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if !slices.Contains(out, value) {
			out = append(out, value)
		}
	}
	return out
}
