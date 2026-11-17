package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

var ErrPairQueued = errors.New("Coding/Reviewer pair queued: two execution slots required")

type pairContextKey struct{}
type pairSlots struct {
	mu            sync.Mutex
	project, plan string
	version       int
	unused        map[string]string
}

// Only supervisor-created context grants can consume a reserved slot or run ID.
func takePairSlot(ctx context.Context, project, plan string, version int, role string) string {
	pair, _ := ctx.Value(pairContextKey{}).(*pairSlots)
	if pair == nil {
		return ""
	}
	pair.mu.Lock()
	defer pair.mu.Unlock()
	if pair.project != project || pair.plan != plan || pair.version != version {
		return ""
	}
	id := pair.unused[role]
	delete(pair.unused, role)
	return id
}

func (e *Engine) reservePair(ctx context.Context, p model.Project) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s := e.settings(ctx)
	if s.Stopped {
		return ErrRunnersStopped
	}
	global, project := pairCapacity(s, p)
	if err := validatePairCapacity(s, p); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	total := 0
	for _, n := range e.active {
		total += n
	}
	if total+2 > global || e.active[p.ID]+2 > project {
		return ErrPairQueued
	}
	e.active[p.ID] += 2
	return nil
}

func pairCapacity(s Settings, p model.Project) (int, int) {
	global, project := s.GlobalSlots, s.ProjectSlots
	if p.Policy.GlobalSlots > 0 && p.Policy.GlobalSlots < global {
		global = p.Policy.GlobalSlots
	}
	if p.Policy.ProjectSlots > 0 {
		project = p.Policy.ProjectSlots
	}
	return global, project
}
func validatePairCapacity(s Settings, p model.Project) error {
	global, project := pairCapacity(s, p)
	if global < 2 || project < 2 {
		return errors.New("Coding/Reviewer pair requires at least two global and project agent slots")
	}
	return nil
}

type executionPair struct {
	PlanID            string `json:"plan_id"`
	PlanVersion       int    `json:"plan_version"`
	Owner             string `json:"owner"`
	CodingRunID       string `json:"coding_run_id"`
	PreparationRunID  string `json:"preparation_run_id"`
	Base              string `json:"base"`
	ConfigurationHash string `json:"configuration_hash"`
}

type reviewerPreparation struct {
	PlanID       string            `json:"plan_id"`
	PlanVersion  int               `json:"plan_version"`
	DocumentHash string            `json:"document_hash"`
	Base         string            `json:"base"`
	AttemptID    string            `json:"attempt_id"`
	Checklist    []preparationItem `json:"checklist"`
}

type preparationItem struct {
	RequirementID string `json:"requirement_id"`
	Inspection    string `json:"inspection"`
}

func (step *taskStep) codeWithPreparation() error {
	key := fmt.Sprintf("%s:%d", step.task.ID, step.task.PlanVersion)
	if err := step.recoverReviewerPreparation(key); err != nil {
		return err
	}
	ready, err := step.reusableReviewerPreparation(key)
	if err != nil {
		return err
	}
	if ready {
		return step.code(step.ctx, step.project, &step.task)
	}
	if err := step.reservePair(step.ctx, step.project); err != nil {
		return err
	}
	pair := &pairSlots{project: step.project.ID, plan: step.task.ID, version: step.task.PlanVersion, unused: map[string]string{"code": store.ID(), "review": store.ID()}}
	defer step.releaseUnusedPair(pair)
	// Establish the shared base before either role can inspect or change source.
	prepPath, err := step.preparePairWorkspace()
	if err != nil {
		return err
	}
	claim, err := step.claimExecutionPair(key, pair)
	if err != nil {
		return err
	}
	defer step.finishUnlaunchedPair(claim)
	return step.runCodingPair(key, prepPath, pair)
}

func (step *taskStep) finishUnlaunchedPair(pair executionPair) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, id := range []string{pair.CodingRunID, pair.PreparationRunID} {
		r, err := step.Store.Get(ctx, "sessions", id)
		if err != nil {
			continue
		}
		a, err := store.Decode[model.Attempt](r)
		if err != nil || a.Owner != pair.Owner || a.Status != "queued" {
			continue
		}
		a.Status, a.Finished, a.Error, a.FinishedAt = "cancelled", true, "pair ended before launch", time.Now().UTC().Format(time.RFC3339Nano)
		_ = step.Store.Put(ctx, "sessions", id, a, r.Version)
	}
}

func parsePreparation(text string, requirements []model.Requirement) ([]preparationItem, error) {
	var output struct {
		Checklist []preparationItem `json:"checklist"`
	}
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		return nil, fmt.Errorf("invalid Reviewer preparation: %w", err)
	}
	seen := map[string]bool{}
	for _, item := range output.Checklist {
		if seen[item.RequirementID] || strings.TrimSpace(item.Inspection) == "" {
			return nil, errors.New("invalid preparation: Reviewer preparation has duplicate or empty checklist entries")
		}
		seen[item.RequirementID] = true
	}
	if len(seen) != len(requirements) {
		return nil, errors.New("invalid preparation: Reviewer preparation must cover every approved requirement")
	}
	for _, requirement := range requirements {
		if !seen[requirement.ID] {
			return nil, fmt.Errorf("invalid preparation: Reviewer preparation missing requirement %s", requirement.ID)
		}
	}
	return output.Checklist, nil
}

func (step *taskStep) reusableReviewerPreparation(key string) (bool, error) {
	if record, err := step.Store.Get(step.ctx, "review_preparations", key); err == nil {
		prepared, err := store.Decode[reviewerPreparation](record)
		if err != nil {
			return false, err
		}
		if prepared.PlanID != step.task.ID || prepared.PlanVersion != step.task.PlanVersion || prepared.DocumentHash != step.task.PlanDocument.ApprovedHash {
			return false, store.ErrConflict
		}
		return true, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return false, err
	}
	return false, nil
}

func (step *taskStep) releaseUnusedPair(pair *pairSlots) {
	pair.mu.Lock()
	defer pair.mu.Unlock()
	for range pair.unused {
		step.release(step.project.ID)
	}
}

func (step *taskStep) preparePairWorkspace() (string, error) {
	if err := step.prepareWorkspace(step.ctx, step.project, &step.task); err != nil {
		return "", err
	}
	sourcePath, err := submissionPath(step.task.Worktree)
	if err != nil {
		return "", err
	}
	prepPath := filepath.Join(filepath.Dir(sourcePath), "review-preparation")
	if err := ensureReviewerSnapshot(step.ctx, step.project.Path, prepPath, step.task.Base); err != nil {
		return "", err
	}
	return prepPath, nil
}

func (step *taskStep) claimExecutionPair(key string, pair *pairSlots) (executionPair, error) {
	claim := executionPair{PlanID: step.task.ID, PlanVersion: step.task.PlanVersion, Owner: step.owner, CodingRunID: pair.unused["code"], PreparationRunID: pair.unused["review"], Base: step.task.Base, ConfigurationHash: step.task.Plan.Approval.ConfigurationHash}
	changes := []store.Change{store.VersionPrecondition("execution_plans", step.task.ID, step.record.Version)}
	expected, err := step.executionPairVersion(key)
	if err != nil {
		return executionPair{}, err
	}
	changes = append(changes, store.Change{Kind: "execution_pairs", ID: key, Value: claim, Expected: expected})
	for role, id := range pair.unused {
		purpose := ""
		if role == "review" {
			purpose = "review_preparation"
		}
		changes = append(changes, store.Change{Kind: "sessions", ID: id, Value: model.Attempt{AttemptID: id, Task: step.task.ID, ProjectID: step.project.ID, PlanVersion: step.task.PlanVersion, Role: role, Purpose: purpose, Owner: step.owner, Status: "queued"}})
	}
	if err := step.Store.Write(step.ctx, changes, nil); err != nil {
		return executionPair{}, err
	}
	return claim, nil
}

func (step *taskStep) executionPairVersion(key string) (int, error) {
	expected := 0
	if record, err := step.Store.Get(step.ctx, "execution_pairs", key); err == nil {
		old, err := store.Decode[executionPair](record)
		if err != nil {
			return 0, err
		}
		if err := step.ensurePairRunsFinished(old); err != nil {
			return 0, err
		}
		expected = record.Version
	} else if !errors.Is(err, store.ErrNotFound) {
		return 0, err
	}
	return expected, nil
}

func (step *taskStep) ensurePairRunsFinished(old executionPair) error {
	for _, id := range []string{old.CodingRunID, old.PreparationRunID} {
		r, err := step.Store.Get(step.ctx, "sessions", id)
		if err != nil {
			return err
		}
		a, err := store.Decode[model.Attempt](r)
		if err != nil {
			return err
		}
		if !a.Finished {
			return store.ErrLeased
		}
	}
	return nil
}

func (step *taskStep) runCodingPair(key, prepPath string, pair *pairSlots) error {
	ctx := context.WithValue(step.ctx, pairContextKey{}, pair)
	group, ctx := errgroup.WithContext(ctx)
	// Build a separate immutable prompt/task before Coding mutates unit state.
	var frozen model.ExecutionPlan
	data, err := json.Marshal(step.task)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &frozen); err != nil {
		return err
	}
	prompt := "Prepare a requirement checklist against this approved specification and base repository snapshot. Do not evaluate an implementation or wait for Coding. Return JSON {\"checklist\":[{\"requirement_id\":\"REQ-ID\",\"inspection\":\"concrete inspection and evidence to check\"}]}, exactly one entry for every approved requirement. Finish immediately after preparation.\n" + jsonText(step.task.Plan)
	group.Go(func() error { return step.code(ctx, step.project, &step.task) })
	group.Go(func() error {
		return step.prepareReviewerChecklist(ctx, key, prepPath, frozen, prompt)
	})
	return group.Wait()
}

func (step *taskStep) prepareReviewerChecklist(ctx context.Context, key, prepPath string, frozen model.ExecutionPlan, prompt string) error {
	out, err := step.agent(ctx, step.project, frozen, provider.Input{Role: "review", Purpose: "review_preparation", ReadOnly: true, CWD: prepPath, Prompt: prompt})
	if err != nil {
		return err
	}
	if err := validateReviewerSnapshot(ctx, step.project.Path, prepPath, frozen.Base); err != nil {
		return fmt.Errorf("invalid preparation: Reviewer preparation snapshot changed: %w", err)
	}
	checklist, err := parsePreparation(out.Text, frozen.Plan.Requirements)
	if err != nil {
		return err
	}
	prepared := reviewerPreparation{PlanID: frozen.ID, PlanVersion: frozen.PlanVersion, DocumentHash: frozen.PlanDocument.ApprovedHash, Base: frozen.Base, AttemptID: out.AttemptID, Checklist: checklist}
	return step.Store.Write(ctx, []store.Change{store.VersionPrecondition("execution_plans", frozen.ID, step.record.Version), {Kind: "review_preparations", ID: key, Value: prepared}}, nil)
}
