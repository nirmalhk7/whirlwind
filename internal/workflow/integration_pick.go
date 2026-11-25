package workflow

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/prompts/agents"
)

// Each Git operation has a durable intent before it can advance the branch.
type integrationCheckpoint struct {
	Plan      string   `json:"plan_id"`
	Version   int      `json:"plan_version"`
	Unit      string   `json:"unit"`
	Source    string   `json:"source"`
	Parent    string   `json:"parent"`
	Commit    string   `json:"commit,omitempty"`
	Tree      string   `json:"resolved_tree,omitempty"`
	Paths     []string `json:"conflicted_paths,omitempty"`
	Index     string   `json:"index,omitempty"`
	AttemptID string   `json:"attempt_id,omitempty"`
}

type integrationPick struct {
	engine  *Engine
	task    *model.ExecutionPlan
	project model.Project
	unit    *model.Unit
	source  string
	version int
	state   integrationCheckpoint
}

func integrationCheckpointID(task model.ExecutionPlan, unit, source string) string {
	return "integration:" + unitKey(task.ID, task.PlanVersion, unit) + ":" + source
}

func (p *integrationPick) git(ctx context.Context, args ...string) (string, error) {
	return codingGit(ctx, p.task.Worktree, args...)
}

func (p *integrationPick) key() string {
	return integrationCheckpointID(*p.task, p.unit.ID, p.source)
}

func (p *integrationPick) save(ctx context.Context) error {
	if err := p.engine.Store.Put(ctx, "checkpoints", p.key(), p.state, p.version); err != nil {
		return err
	}
	p.version++
	return nil
}

func (p *integrationPick) run(ctx context.Context) error {
	head, err := p.git(ctx, gitRevParseCommand, "HEAD")
	if err != nil {
		return err
	}
	pending, err := p.git(ctx, gitRevParseCommand, "--revs-only", "CHERRY_PICK_HEAD")
	if err != nil {
		return err
	}
	if err := p.load(ctx, head, pending); err != nil {
		return err
	}
	if p.state.Commit != "" {
		if err := p.verifyHistory(ctx); err != nil {
			return err
		}
	} else if err := p.finish(ctx, head, pending); err != nil {
		return err
	}
	if p.state.AttemptID != "" {
		p.task.LastAttemptID = p.state.AttemptID
		if !slices.Contains(p.task.ImplementationAttempts, p.state.AttemptID) {
			p.task.ImplementationAttempts = append(p.task.ImplementationAttempts, p.state.AttemptID)
		}
	}
	return nil
}

func (p *integrationPick) load(ctx context.Context, head, pending string) error {
	record, err := p.engine.Store.Get(ctx, "checkpoints", p.key())
	if err == nil {
		p.version = record.Version
		p.state, err = store.Decode[integrationCheckpoint](record)
		if err == nil && (p.state.Plan != p.task.ID || p.state.Version != p.task.PlanVersion || p.state.Unit != p.unit.ID || p.state.Source != p.source || p.state.Parent == "") {
			return errors.New("integration checkpoint does not match the approved unit")
		}
		return err
	}
	if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if pending != "" {
		return fmt.Errorf("unrecorded cherry-pick %s requires reconciliation", pending)
	}
	p.state = integrationCheckpoint{Plan: p.task.ID, Version: p.task.PlanVersion, Unit: p.unit.ID, Source: p.source, Parent: head}
	return p.save(ctx)
}

func (p *integrationPick) finish(ctx context.Context, head, pending string) error {
	if pending != "" && (pending != p.source || head != p.state.Parent) {
		return errors.New("pending cherry-pick differs from the recorded integration")
	}
	if head == p.state.Parent {
		if pending == "" {
			if err := p.start(ctx); err != nil {
				return err
			}
		} else if err := p.resolve(ctx); err != nil {
			return err
		}
		var err error
		head, err = p.git(ctx, gitRevParseCommand, "HEAD")
		if err != nil {
			return err
		}
	}
	if err := p.validateResult(ctx, head); err != nil {
		return err
	}
	p.state.Commit = head
	return p.save(ctx)
}

func (p *integrationPick) start(ctx context.Context) error {
	paths, err := codingChangedPaths(ctx, p.task.Worktree)
	if err != nil {
		return err
	}
	if len(paths) != 0 || len(p.state.Paths) != 0 {
		return errors.New("integration worktree has unrecorded changes")
	}
	_, err = p.git(ctx, "cherry-pick", "-x", "--keep-redundant-commits", p.source)
	if err == nil {
		return nil
	}
	pending, inspectErr := p.git(ctx, gitRevParseCommand, "--revs-only", "CHERRY_PICK_HEAD")
	if inspectErr != nil || pending != p.source {
		return errors.Join(err, inspectErr)
	}
	return p.resolve(ctx)
}

func (p *integrationPick) validateResult(ctx context.Context, head string) error {
	parents, err := p.git(ctx, "rev-list", "--parents", "-n1", head)
	if err != nil || parents != head+" "+p.state.Parent {
		return errors.Join(err, errors.New("integration result does not descend directly from the recorded parent"))
	}
	if p.state.Tree != "" {
		tree, err := p.git(ctx, gitRevParseCommand, head+"^{tree}")
		if err != nil || tree != p.state.Tree {
			return errors.Join(err, errors.New("integration result differs from the staged resolution"))
		}
		return nil
	}
	message, err := p.git(ctx, "show", "-s", "--format=%B", head)
	if err != nil || !strings.HasSuffix(message, "(cherry picked from commit "+p.source+")") {
		return errors.Join(err, errors.New("integration result has no matching source commit"))
	}
	return nil
}

func (p *integrationPick) verifyHistory(ctx context.Context) error {
	_, err := p.git(ctx, "merge-base", "--is-ancestor", p.state.Commit, "HEAD")
	return err
}

func (p *integrationPick) resolve(ctx context.Context) error {
	if err := p.stageResolution(ctx); err != nil {
		return err
	}
	if _, err := p.git(ctx, append([]string{"diff", "--cached", "--check", "--"}, p.state.Paths...)...); err != nil {
		return err
	}
	tree, err := p.git(ctx, "write-tree")
	if err != nil {
		return err
	}
	if p.state.Tree != "" && p.state.Tree != tree {
		return errors.New("staged resolution changed before continuation")
	}
	if p.state.Tree == "" {
		p.state.Tree = tree
		if err := p.save(ctx); err != nil {
			return err
		}
	}
	_, err = p.git(ctx, "cherry-pick", "--continue")
	return err
}

func (p *integrationPick) stageResolution(ctx context.Context) error {
	unmerged, err := p.git(ctx, "diff", "--name-only", "--diff-filter=U", "-z", "--")
	if err != nil {
		return err
	}
	if unmerged == "" && p.state.AttemptID == "" {
		return errors.New("pending cherry-pick has no recorded conflict resolution")
	}
	if err := p.retainConflictPaths(ctx, unmerged); err != nil {
		return err
	}
	scoped, err := p.conflictProject()
	if err != nil {
		return err
	}
	if err := p.checkResolution(ctx); err != nil {
		return err
	}
	if unmerged == "" {
		return nil
	}
	if p.state.AttemptID == "" {
		if err := p.askCoder(ctx, scoped); err != nil {
			return err
		}
	}
	args := append([]string{"add", "--all", "--"}, p.state.Paths...)
	_, err = p.git(ctx, args...)
	return err
}

func (p *integrationPick) retainConflictPaths(ctx context.Context, unmerged string) error {
	if unmerged == "" {
		return nil
	}
	paths := strings.Split(strings.TrimSuffix(unmerged, "\x00"), "\x00")
	if p.state.Paths == nil {
		p.state.Paths = paths
		index, err := p.outsideIndex(ctx)
		p.state.Index = index
		if err != nil {
			return err
		}
		if err := p.save(ctx); err != nil {
			return err
		}
	}
	if !slices.Equal(paths, p.state.Paths) {
		return errors.New("pending conflict paths changed during integration")
	}
	return nil
}

func (p *integrationPick) conflictProject() (model.Project, error) {
	project := p.project
	if err := ValidateUnitScopes(project, []model.Unit{*p.unit}); err != nil {
		return project, err
	}
	if len(p.unit.AllowedPaths) > 0 {
		project.Policy.AllowedPaths = p.unit.AllowedPaths
	}
	if err := validateCodingPaths(project.Policy, p.state.Paths); err != nil {
		return project, err
	}
	for _, path := range p.state.Paths {
		if strings.ContainsAny(path, "*?[]{}\\\n") {
			return project, fmt.Errorf("cannot express literal conflict path %q in the Coding write scope", path)
		}
	}
	project.Policy.AllowedPaths = p.state.Paths
	return project, nil
}

func (p *integrationPick) askCoder(ctx context.Context, project model.Project) error {
	prompt, err := agents.Render("integrate", map[string]string{"Unit": jsonText(p.unit), "Paths": jsonText(p.state.Paths), "Plan": jsonText(p.task.Plan)})
	if err != nil {
		return err
	}
	out, err := p.engine.agent(ctx, project, *p.task, provider.Input{UnitID: p.unit.ID, Purpose: "integration", Role: "code", CWD: p.task.Worktree, Prompt: prompt})
	if err != nil {
		return err
	}
	if err := p.checkResolution(ctx); err != nil {
		return err
	}
	p.state.AttemptID = out.AttemptID
	return p.save(ctx)
}

func (p *integrationPick) outsideIndex(ctx context.Context) (string, error) {
	index, err := p.git(ctx, "ls-files", "--stage", "-z")
	if err != nil {
		return "", err
	}
	var outside []string
	for entry := range strings.SplitSeq(index, "\x00") {
		_, path, found := strings.Cut(entry, "\t")
		if found && !slices.Contains(p.state.Paths, path) {
			outside = append(outside, entry)
		}
	}
	return evidenceDigest(outside), nil
}

func (p *integrationPick) checkResolution(ctx context.Context) error {
	head, err := p.git(ctx, gitRevParseCommand, "HEAD", "CHERRY_PICK_HEAD")
	if err != nil || head != p.state.Parent+"\n"+p.source {
		return errors.Join(err, errors.New("cherry-pick state changed during conflict resolution"))
	}
	index, err := p.outsideIndex(ctx)
	if err != nil || index != p.state.Index {
		return errors.Join(err, errors.New("the Git index changed outside the conflict paths"))
	}
	unstaged, err := p.git(ctx, "diff", "--name-only", "--no-renames", "-z", "--")
	if err != nil {
		return err
	}
	untracked, err := p.git(ctx, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return err
	}
	for path := range strings.SplitSeq(unstaged+untracked, "\x00") {
		if path != "" && !slices.Contains(p.state.Paths, path) {
			return fmt.Errorf("out-of-scope conflict edit: %s", path)
		}
	}
	return nil
}
