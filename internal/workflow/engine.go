package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	ctxengine "whirlwind/internal/context"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

type Engine struct {
	Store      *store.Store
	GitHub     github.Client
	Root       string
	Adapter    func(model.Provider) provider.Adapter
	owner      string
	mu         sync.Mutex
	running    map[string]context.CancelFunc
	active     map[string]int
	gitMu      sync.Mutex
	background sync.WaitGroup
}

// New constructs the workflow engine with default process adapters, a unique lease owner, and execution tracking.
func New(s *store.Store, g github.Client, root string) *Engine {
	return &Engine{Store: s, GitHub: g, Root: root, Adapter: func(p model.Provider) provider.Adapter { return provider.ProcessAdapter{Provider: p} }, owner: store.ID(), running: map[string]context.CancelFunc{}, active: map[string]int{}}
}

// Run dispatches task steps, schedules daily ideation and reconciliation, and cancels and waits for background work on shutdown.
func (e *Engine) Run(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	nextReconcile := time.Now().Add(5 * time.Minute)
	e.background.Add(1)
	go func() { defer e.background.Done(); e.Reconcile(ctx) }()
	for {
		select {
		case <-ctx.Done():
			e.mu.Lock()
			for _, cancel := range e.running {
				cancel()
			}
			e.mu.Unlock()
			e.background.Wait()
			return
		case <-ticker.C:
			e.dispatch(ctx)
			if !time.Now().Before(nextReconcile) {
				e.background.Add(1)
				go func() { defer e.background.Done(); e.Reconcile(ctx) }()
				settings := e.settings(ctx)
				nextReconcile = time.Now().Add(time.Duration(settings.ReconciliationSeconds) * time.Second)
			}
			e.background.Add(1)
			go func() { defer e.background.Done(); e.Daily(ctx) }()
		}
	}
}

// dispatch starts one step per runnable task while respecting per-project task capacity and avoiding duplicate active steps.
func (e *Engine) dispatch(ctx context.Context) {
	records, err := e.Store.List(ctx, "tasks")
	if err != nil {
		return
	}
	counts := map[string]int{}
	e.mu.Lock()
	for _, record := range records {
		if _, running := e.running[record.ID]; running {
			task, _ := store.Decode[model.Task](record)
			counts[task.ProjectID]++
		}
	}
	e.mu.Unlock()
	for _, r := range records {
		t, err := store.Decode[model.Task](r)
		if err != nil || t.Stage == "paused" || t.Stage == "questions" || t.Stage == "approval" || t.Stage == "pr_created" || t.Stage == "complete" {
			continue
		}
		pRec, err := e.Store.Get(ctx, "projects", t.ProjectID)
		if err != nil {
			continue
		}
		p, _ := store.Decode[model.Project](pRec)
		limit := p.Policy.ProjectSlots
		if limit <= 0 {
			limit = e.settings(ctx).ProjectSlots
		}
		if counts[p.ID] >= limit {
			continue
		}
		e.mu.Lock()
		_, busy := e.running[t.ID]
		if !busy {
			run, cancel := context.WithCancel(ctx)
			e.running[t.ID] = cancel
			e.background.Add(1)
			counts[p.ID]++
			go func(id string) {
				defer e.background.Done()
				defer func() { e.mu.Lock(); delete(e.running, id); e.mu.Unlock() }()
				if err := e.Step(run, id); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, store.ErrLeased) && !errors.Is(err, store.ErrConflict) {
					e.fail(ctx, id, err)
				}
			}(t.ID)
		}
		e.mu.Unlock()
	}
}

// Stop cancels an active task step so its agent processes can terminate.
func (e *Engine) Stop(id string) {
	e.mu.Lock()
	if c := e.running[id]; c != nil {
		c()
	}
	e.mu.Unlock()
}

// fail pauses a failed task at its resumable stage and creates an Inbox recovery notification.
func (e *Engine) fail(ctx context.Context, id string, err error) {
	r, x := e.Store.Get(ctx, "tasks", id)
	if x != nil {
		return
	}
	t, x := store.Decode[model.Task](r)
	if x != nil {
		return
	}
	if t.Stage != "paused" {
		t.ResumeStage = t.Stage
	}
	t.Stage = "paused"
	t.Error = err.Error()
	if e.Store.Put(ctx, "tasks", id, t, r.Version) == nil {
		_ = e.Store.Notify(ctx, id, "recovery", err.Error())
	}
}

// Project loads and decodes the registered project used by a task.
func (e *Engine) Project(ctx context.Context, id string) (model.Project, error) {
	r, err := e.Store.Get(ctx, "projects", id)
	if err != nil {
		return model.Project{}, err
	}
	return store.Decode[model.Project](r)
}

// renewLease keeps a minute-long lease alive, cancelling work if renewal or its optional validity check fails.
func (e *Engine) renewLease(ctx context.Context, cancel context.CancelFunc, id string, fence int64, valid func() bool) {
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if e.Store.Renew(ctx, id, e.owner, fence, time.Minute) != nil || (valid != nil && !valid()) {
					cancel()
					return
				}
			}
		}
	}()
}

// Step claims and renews a task lease, verifies approved policy, and advances one planning, coding, repair, review, or publication stage.
func (e *Engine) Step(ctx context.Context, id string) error {
	lease, err := e.Store.Claim(ctx, "task:"+id, e.owner, time.Minute)
	if err != nil {
		return err
	}
	defer e.Store.Release(context.Background(), "task:"+id, e.owner, lease.Fence)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	r, err := e.Store.Get(ctx, "tasks", id)
	if err != nil {
		return err
	}
	t, err := store.Decode[model.Task](r)
	if err != nil {
		return err
	}
	p, err := e.Project(ctx, t.ProjectID)
	if err != nil {
		return err
	}
	_, hash, err := policy.Load(p.Path)
	if err != nil {
		return err
	}
	if hash != p.PolicyHash {
		return errors.New("committed policy changed; confirm updated project policy before continuing")
	}
	e.renewLease(ctx, cancel, "task:"+id, lease.Fence, func() bool {
		current, err := e.Store.Get(ctx, "tasks", id)
		return err == nil && current.Version == r.Version
	})
	switch t.Stage {
	case "planning":
		var plan model.Plan
		prompt := "Return only JSON matching {objective:string,acceptance_criteria:string[],questions:[{id,text,answer}],units:[{id,objective,dependencies:string[],allowed_paths:string[]}]}. Ask questions only when necessary. Keep small plans one unit. Do not change files.\nIssue:\n" + t.Issue.Title + "\n" + t.Issue.Body
		if t.Plan != nil {
			prompt += "\nAnswered questions: " + jsonText(t.Plan.Questions) + "\nIncorporate all answers. Do not repeat answered questions."
		}
		planningPath := filepath.Join(e.Root, "planning", t.ID, fmt.Sprintf("v%d", t.PlanVersion))
		base, x := e.base(ctx, p)
		if x != nil {
			return x
		}
		if x = e.worktree(ctx, p.Path, planningPath, fmt.Sprintf("codex/whirlwind-plan-%s-v%d", t.ID, t.PlanVersion), base); x != nil {
			return x
		}
		out, err := e.agent(ctx, p, t, "plan", planningPath, prompt, true, "")
		if err != nil {
			return err
		}
		if err = provider.JSON(out.Text, &plan); err != nil {
			return err
		}
		if plan.Objective == "" || len(plan.AcceptanceCriteria) == 0 {
			return errors.New("planner omitted objective or acceptance criteria")
		}
		if len(plan.Units) == 0 {
			plan.Units = []model.Unit{{ID: "main", Objective: plan.Objective}}
		}
		if err = ValidateUnits(plan.Units); err != nil {
			return err
		}
		t.Plan = &plan
		t.Stage = "coding"
		if t.Issue.Number == 0 {
			t.Stage = "plan_ready"
		}
		for _, q := range plan.Questions {
			if q.ID == "" || q.Text == "" {
				return errors.New("invalid planner question")
			}
			if q.Answer == "" {
				t.Stage = "questions"
			}
		}
		t.Handoff.NextAction = "implement plan"
		planKey := fmt.Sprintf("%s:%d", t.ID, t.PlanVersion)
		previousPlan, _ := e.Store.Get(ctx, "plans", planKey)
		if err = e.Store.Put(ctx, "plans", planKey, plan, previousPlan.Version); err != nil {
			return err
		}
	case "plan_ready":
		if t.Plan == nil {
			return errors.New("ready plan missing")
		}
		body := t.Plan.Objective + "\n\nAcceptance criteria:\n" + jsonText(t.Plan.AcceptanceCriteria) + "\n\nImplementation units:\n" + jsonText(t.Plan.Units)
		i, x := e.GitHub.CreateIssue(ctx, p.Repo, t.Issue.Title, body, "<!-- whirlwind-plan:"+t.ID+" -->")
		if x != nil {
			return x
		}
		t.Issue = i
		t.Stage = "coding"
		return e.Store.BindIssue(ctx, p.Repo, t, r.Version)
	case "coding":
		if t.Plan == nil {
			return errors.New("plan required")
		}
		if err = e.code(ctx, p, &t); err != nil {
			return err
		}
		if t.Stage == "coding" {
			t.Stage = "review"
			t.ChecksPassed = true
		}
		if t.ChecksPassed {
			_ = e.Store.Notify(ctx, t.ID, "review", "Implementation verified; Reviewer Agent starts next")
		}
	case "review":
		if err = e.verify(ctx, p, &t); err != nil {
			t.ChecksPassed = false
			t.Stage = "repair"
			t.Error = err.Error()
			return e.Store.Put(ctx, "tasks", t.ID, t, r.Version)
		}
		beforeReview, x := github.Run(ctx, t.Worktree, "git", "rev-parse", "HEAD")
		if x != nil {
			return x
		}
		diff, err := github.Run(ctx, t.Worktree, "git", "diff", t.Base+"...HEAD")
		if err != nil {
			return err
		}
		prompt := "Read-only review. Return JSON {score:number,findings:[{id,file,line,message,blocking,resolved}],summary:string}. Score 0..10. Examine full diff against acceptance criteria, inaccessible code and out-of-scope code. Keep stable IDs for unchanged findings. Explicitly mark previous findings resolved when corrected; never omit unresolved blockers.\nPlan:\n" + jsonText(t.Plan) + "\nPrevious findings:\n" + e.latestReview(ctx, fmt.Sprintf("%s:%d", t.ID, t.PlanVersion)) + "\nDiff:\n" + diff
		out, err := e.agent(ctx, p, t, "review", t.Worktree, prompt, true, "")
		if err != nil {
			return err
		}
		var review model.Review
		if err = provider.JSON(out.Text, &review); err != nil {
			return err
		}
		if review.Score < 0 || review.Score > 10 {
			return errors.New("review score outside 0..10")
		}
		for n := range review.Findings {
			f := &review.Findings[n]
			if f.Message == "" {
				return errors.New("review finding lacks message")
			}
			if f.ID == "" {
				sum := sha256.Sum256([]byte(f.File + ":" + f.Message))
				f.ID = hex.EncodeToString(sum[:8])
			}
		}
		if t.Risk == "high" || t.Risk == "critical" {
			for _, perspective := range []string{"security", "architecture"} {
				special, x := e.agent(ctx, p, t, "review", t.Worktree, prompt+"\nPerform dedicated "+perspective+" review; return same JSON contract.", true, "")
				if x != nil {
					return x
				}
				var extra model.Review
				if x = provider.JSON(special.Text, &extra); x != nil {
					return x
				}
				if extra.Score < 0 || extra.Score > 10 {
					return errors.New("invalid specialist score")
				}
				if extra.Score < review.Score {
					review.Score = extra.Score
				}
				review.Summary += "\n" + perspective + ": " + extra.Summary
				for n := range extra.Findings {
					f := &extra.Findings[n]
					if f.ID == "" {
						sum := sha256.Sum256([]byte(perspective + f.File + f.Message))
						f.ID = hex.EncodeToString(sum[:8])
					}
				}
				review.Findings = MergeFindings(review.Findings, extra.Findings)
			}
		}
		var previousReview model.Review
		_ = json.Unmarshal([]byte(e.latestReview(ctx, fmt.Sprintf("%s:%d", t.ID, t.PlanVersion))), &previousReview)
		review.Findings = MergeFindings(previousReview.Findings, review.Findings)
		afterReview, x := github.Run(ctx, t.Worktree, "git", "rev-parse", "HEAD")
		if x != nil {
			return x
		}
		if afterReview != beforeReview {
			return errors.New("read-only reviewer changed commit")
		}
		dirty, x := github.Run(ctx, t.Worktree, "git", "status", "--porcelain", "-uall")
		if x != nil {
			return x
		}
		if dirty != "" {
			return errors.New("read-only reviewer changed working tree")
		}
		t.ReviewedCommit = beforeReview
		t.ReviewPasses++
		t.Score = review.Score
		if err = e.Store.Put(ctx, "reviews", fmt.Sprintf("%s:%d:%d", t.ID, t.PlanVersion, t.ReviewPasses), review, 0); err != nil {
			return err
		}
		for _, finding := range review.Findings {
			key := fmt.Sprintf("%s:%d:%s", t.ID, t.PlanVersion, finding.ID)
			old, _ := e.Store.Get(ctx, "findings", key)
			if err = e.Store.Put(ctx, "findings", key, finding, old.Version); err != nil {
				return err
			}
		}
		if Accepted(review, t.ChecksPassed) {
			t.Handoff.CompletedCriteria = append([]string{}, t.Plan.AcceptanceCriteria...)
			if t.Risk == "critical" && !t.Approved {
				t.Stage = "approval"
				_ = e.Store.Notify(ctx, t.ID, "critical_approval", "Critical change verified and reviewed; approve PR publication")
			} else {
				t.Stage = "publishing"
			}
		} else {
			t.Stage = "repair"
			t.Handoff.NextAction = "correct unresolved review findings"
			if err = e.progress(ctx, &t); err != nil {
				return err
			}
		}
	case "repair":
		t.Approved = false
		prompt := "Correct unresolved review findings and satisfy plan. Do not publish or change policy.\nPlan:\n" + jsonText(t.Plan) + "\nFindings:\n" + e.latestReview(ctx, fmt.Sprintf("%s:%d", t.ID, t.PlanVersion)) + "\nHandoff:\n" + jsonText(t.Handoff)
		if _, err = e.agent(ctx, p, t, "code", t.Worktree, prompt, false, ""); err != nil {
			return err
		}
		if err = e.commit(ctx, p, t.Worktree, "Address review findings"); err != nil {
			return err
		}
		if err = e.verify(ctx, p, &t); err != nil {
			t.ChecksPassed = false
			t.Stage = "repair"
			t.Error = err.Error()
			if x := e.progress(ctx, &t); x != nil {
				return x
			}
		} else {
			t.ChecksPassed = true
			t.Stage = "review"
			t.Error = ""
		}
	case "publishing":
		if p.DefaultBranch != "" {
			latest, x := e.GitHub.Issue(ctx, p.Repo, t.Issue.Number)
			if x != nil {
				return x
			}
			if (!latest.Eligible() && !t.EligibilityOverride) || latest.Title != t.Issue.Title || latest.Body != t.Issue.Body {
				_, x = e.Ingest(ctx, p, latest)
				if x != nil {
					return x
				}
				return store.ErrConflict
			}
		}
		reviewed, checkErr := github.Run(ctx, t.Worktree, "git", "rev-parse", "HEAD")
		if checkErr != nil {
			return checkErr
		}
		if reviewed != t.ReviewedCommit {
			t.Stage = "review"
			t.Approved = false
			return e.Store.Put(ctx, "tasks", t.ID, t, r.Version)
		}
		if !t.ChecksPassed || t.Score < 9 || (t.Risk == "critical" && !t.Approved) {
			return errors.New("publication acceptance gate failed")
		}
		previousRisk := t.Risk
		if err = e.verify(ctx, p, &t); err != nil {
			return err
		}
		if t.Risk != previousRisk {
			t.Stage = "review"
			t.Approved = false
			return e.Store.Put(ctx, "tasks", t.ID, t, r.Version)
		}
		if t.Risk == "critical" && !t.Approved {
			t.Stage = "approval"
			return e.Store.Put(ctx, "tasks", t.ID, t, r.Version)
		}
		base, err := github.Run(ctx, p.Path, "git", "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
		if err != nil {
			base, err = github.Run(ctx, p.Path, "git", "branch", "--show-current")
		}
		if err != nil {
			return err
		}
		base = strings.TrimPrefix(base, "origin/")
		if p.DefaultBranch != "" {
			base = p.DefaultBranch
		}
		var description strings.Builder
		fmt.Fprintf(&description, "%s\n\nCloses #%d\n\n## Implementation\n", t.Plan.Objective, t.Issue.Number)
		for _, unit := range t.Plan.Units {
			fmt.Fprintf(&description, "- %s\n", unit.Objective)
		}
		description.WriteString("\n## Acceptance criteria\n")
		for _, criterion := range t.Plan.AcceptanceCriteria {
			fmt.Fprintf(&description, "- %s\n", criterion)
		}
		description.WriteString("\n## Validation\n")
		verificationRecords, _ := e.Store.List(ctx, "verification")
		var verified verification.Report
		for _, record := range verificationRecords {
			var v struct {
				Task    string              `json:"task"`
				Version int                 `json:"plan_version"`
				Report  verification.Report `json:"report"`
			}
			if json.Unmarshal(record.Data, &v) == nil && v.Task == t.ID && v.Version == t.PlanVersion {
				verified = v.Report
			}
		}
		for _, check := range verified.Results {
			fmt.Fprintf(&description, "- %s: passed\n", check.Name)
		}
		var review model.Review
		_ = json.Unmarshal([]byte(e.latestReview(ctx, fmt.Sprintf("%s:%d", t.ID, t.PlanVersion))), &review)
		fmt.Fprintf(&description, "\nReview score: %.1f/10; no unresolved blocking findings.\n\n%s\n\n<!-- whirlwind-task:%s -->", t.Score, review.Summary, t.ID)
		body := description.String()

		url, err := e.GitHub.CreatePR(ctx, t.Worktree, p.Repo, t.Branch, base, t.Issue.Title, body)
		if err != nil {
			return err
		}
		t.PR = url
		t.Stage = "pr_created"
		t.Handoff.NextAction = "user reviews and merges PR"
		_ = e.Store.Notify(ctx, t.ID, "pr", url)
	default:
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return e.Store.Put(ctx, "tasks", t.ID, t, r.Version)
}

// jsonText serializes internal context to JSON text for agent prompts, ignoring marshal errors.
func jsonText(v any) string { b, _ := json.Marshal(v); return string(b) }

// latestReview returns the most recently updated review payload for a task and plan version, or an empty JSON array.
func (e *Engine) latestReview(ctx context.Context, id string) string {
	rs, _ := e.Store.List(ctx, "reviews")
	value := "[]"
	for _, r := range rs {
		if strings.HasPrefix(r.ID, id+":") {
			value = string(r.Data)
		}
	}
	return value
}

// agent reserves capacity, selects a capable provider, assembles context and handoff, records sessions and usage, and switches providers on quota or login failure.
func (e *Engine) agent(ctx context.Context, p model.Project, t model.Task, role, cwd, prompt string, readonly bool, session string) (provider.Output, error) {
	if p.Policy.RunTimeoutSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(p.Policy.RunTimeoutSeconds)*time.Second)
		defer cancel()
	}
	canonical, canonicalErr := filepath.EvalSymlinks(cwd)
	if canonicalErr != nil {
		return provider.Output{}, canonicalErr
	}
	cwd = canonical
	if err := e.acquire(ctx, p); err != nil {
		return provider.Output{}, err
	}
	defer e.release(p.ID)
	t.Handoff.Version = 1
	t.Handoff.Worktree = cwd
	if branch, x := github.Run(ctx, cwd, "git", "branch", "--show-current"); x == nil {
		t.Handoff.Branch = branch
	}
	prompt += "\nVersioned handoff:\n" + jsonText(t.Handoff)
	rs, err := e.Store.List(ctx, "providers")
	if err != nil {
		return provider.Output{}, err
	}
	ps := []model.Provider{}
	for _, r := range rs {
		x, _ := store.Decode[model.Provider](r)
		ps = append(ps, x)
	}
	index, err := e.index(ctx, p)
	if err != nil {
		return provider.Output{}, err
	}
	if role == "code" || role == "review" || role == "plan" {
		if head, x := github.Run(ctx, cwd, "git", "rev-parse", "HEAD"); x == nil && head != index.Commit {
			index, err = ctxengine.Build(ctx, cwd, &index, head)
			if err != nil {
				return provider.Output{}, err
			}
		}
	}
	snapshotKey := p.ID + ":" + index.Commit
	if _, x := e.Store.Get(ctx, "index_history", snapshotKey); errors.Is(x, store.ErrNotFound) {
		if x = e.Store.Put(ctx, "index_history", snapshotKey, index, 0); x != nil && !errors.Is(x, store.ErrConflict) {
			return provider.Output{}, x
		}
	}
	prompt += "\nSelected repository context:\n" + index.Package(t.Issue.Title+" "+t.Issue.Body+" "+prompt)
	if p.Policy.Embeddings != nil && p.Policy.Embeddings.Enabled && index.EmbeddingValidated {
		vectors, x := ctxengine.Embed(ctx, *p.Policy.Embeddings, []string{t.Issue.Title + " " + t.Issue.Body})
		if x != nil {
			return provider.Output{}, x
		}
		prompt += "\nEvaluated embedding retrieval:\n" + jsonText(index.VectorSearch(vectors[0], 5))
	}
	for attempt := 0; attempt < len(ps); attempt++ {
		preferences := append([]string{}, p.Policy.Providers...)
		if t.Recoveries > 0 {
			candidates := []string{}
			for _, candidate := range ps {
				if candidate.Available {
					candidates = append(candidates, candidate.ID)
				}
			}
			if len(candidates) > 0 {
				offset := t.Recoveries % len(candidates)
				preferences = append(candidates[offset:], candidates[:offset]...)
			}
		}
		chosen, routeErr := provider.Route(ps, role, preferences, e.outcomes(ctx, role, index.Category()))
		if routeErr != nil {
			return provider.Output{}, routeErr
		}
		if session == "" && t.Recoveries == 0 {
			session = e.resumeSession(ctx, t.ID, role, chosen.ID, cwd, t.PlanVersion)
		}
		sid := store.ID()
		sessionState := map[string]any{"task": t.ID, "role": role, "provider": chosen.ID, "session_id": session, "cwd": cwd, "plan_version": t.PlanVersion, "finished": false, "context_commit": index.Commit, "context_index_version": index.Version}
		if err := e.Store.Put(ctx, "sessions", sid, sessionState, 0); err != nil {
			return provider.Output{}, err
		}
		hooks := provider.Hooks{Questions: func(c context.Context, questions []model.Question) (map[string]string, error) {
			set := model.QuestionSet{ID: store.ID(), TaskID: t.ID, Provider: chosen.ID, Questions: questions, Status: "pending", PlanVersion: t.PlanVersion}
			if x := e.Store.Put(c, "questions", set.ID, set, 0); x != nil {
				return nil, x
			}
			_ = e.Store.Notify(c, t.ID, "question", "Agent clarification awaits answer: "+set.ID)
			defer func() {
				if c.Err() != nil {
					record, x := e.Store.Get(context.Background(), "questions", set.ID)
					if x == nil {
						pending, _ := store.Decode[model.QuestionSet](record)
						if pending.Status == "pending" {
							pending.Status = "cancelled"
							_ = e.Store.Put(context.Background(), "questions", set.ID, pending, record.Version)
						}
					}
				}
			}()
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-c.Done():
					return nil, c.Err()
				case <-ticker.C:
					record, x := e.Store.Get(c, "questions", set.ID)
					if x != nil {
						return nil, x
					}
					value, x := store.Decode[model.QuestionSet](record)
					if x != nil {
						return nil, x
					}
					if value.Status == "answered" {
						return value.Answers, nil
					}
				}
			}
		}, Event: func(kind string, data any) {
			if kind == "whirlwind/session" {
				b, _ := json.Marshal(data)
				var current struct {
					ID string `json:"session_id"`
				}
				_ = json.Unmarshal(b, &current)
				record, _ := e.Store.Get(ctx, "sessions", sid)
				sessionState["session_id"] = current.ID
				_ = e.Store.Put(ctx, "sessions", sid, sessionState, record.Version)
				return
			}
			if kind == "whirlwind/quota" {
				state, reset, x := provider.Quota(data)
				if x == nil {
					record, x := e.Store.Get(ctx, "providers", chosen.ID)
					if x == nil {
						value, _ := store.Decode[model.Provider](record)
						value.Quota = state
						value.ResetAt = reset
						_ = e.Store.Put(ctx, "providers", value.ID, value, record.Version)
					}
				}
				return
			}

			if kind == "session/update" || kind == "item/agentMessage/delta" {
				return
			}
			_ = e.Store.Put(ctx, "agent_events", store.ID(), map[string]any{"task": t.ID, "provider": chosen.ID, "kind": kind, "data": data}, 0)
		}, Permission: func(c context.Context, perm provider.Permission) (bool, error) {
			if readonly {
				return false, nil
			}
			auto := len(perm.Command) > 0 && policy.AllowedCommand(p.Policy, perm.Command)
			if len(perm.Paths) > 0 {
				if len(perm.Command) == 0 {
					auto = true
				}
				for _, path := range perm.Paths {
					relative := path
					if filepath.IsAbs(path) {
						var pathErr error
						relative, pathErr = filepath.Rel(cwd, path)
						if pathErr != nil {
							auto = false
							continue
						}
					}
					if !policy.AllowedPath(p.Policy, relative) {
						auto = false
					}
				}
			}
			if auto {
				return true, nil
			}
			id := store.ID()
			v := map[string]any{"id": id, "task": t.ID, "provider": chosen.ID, "request": perm, "decision": "pending", "plan_version": t.PlanVersion}
			if err := e.Store.Put(c, "permissions", id, v, 0); err != nil {
				return false, err
			}
			defer func() {
				if c.Err() != nil {
					record, x := e.Store.Get(context.Background(), "permissions", id)
					if x == nil {
						var value map[string]any
						_ = json.Unmarshal(record.Data, &value)
						if value["decision"] == "pending" {
							value["decision"] = "cancelled"
							_ = e.Store.Put(context.Background(), "permissions", id, value, record.Version)
						}
					}
				}
			}()
			_ = e.Store.Notify(c, t.ID, "permission", "Tool permission awaits decision: "+id)
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-c.Done():
					return false, c.Err()
				case <-ticker.C:
					r, err := e.Store.Get(c, "permissions", id)
					if err != nil {
						return false, err
					}
					var d struct {
						Decision string `json:"decision"`
					}
					if err = json.Unmarshal(r.Data, &d); err != nil {
						return false, err
					}
					if d.Decision != "pending" {
						return d.Decision == "allow", nil
					}
				}
			}
		}}
		start := time.Now()
		output, err := e.Adapter(chosen).Run(ctx, provider.Input{Role: role, CWD: cwd, Prompt: prompt, SessionID: session, Policy: p.Policy, ReadOnly: readonly}, hooks)
		record, _ := e.Store.Get(context.Background(), "sessions", sid)
		sessionState["session_id"] = output.SessionID
		sessionState["finished"] = true
		sessionState["summary"] = output.Text
		_ = e.Store.Put(context.Background(), "sessions", sid, sessionState, record.Version)
		_ = e.Store.Put(context.Background(), "metrics", store.ID(), map[string]any{"task": t.ID, "role": role, "provider": chosen.ID, "tokens": output.Tokens, "cost": output.Cost, "elapsed_ms": time.Since(start).Milliseconds(), "success": err == nil, "review_attempts": t.ReviewPasses, "category": index.Category(), "risk": t.Risk}, 0)
		record, _ = e.Store.Get(context.Background(), "providers", chosen.ID)
		latest, _ := store.Decode[model.Provider](record)
		quotaFailure := latest.Quota == "exhausted"
		authFailure := false
		if err != nil {
			message := strings.ToLower(err.Error())
			for _, marker := range []string{"quota", "rate limit", "usage_limit_reached", "usagelimitreached"} {
				quotaFailure = quotaFailure || strings.Contains(message, marker)
			}
			for _, marker := range []string{"authentication", "not logged in", "sign in", "login required", "unauthorized"} {
				authFailure = authFailure || strings.Contains(message, marker)
			}
		}
		if err != nil && (quotaFailure || authFailure) {
			chosen = latest
			if authFailure {
				chosen.Available = false
				chosen.Limitation = "Authentication required; use provider login and recheck setup"
			} else {
				chosen.Quota = "exhausted"
			}
			_ = e.Store.Put(context.Background(), "providers", chosen.ID, chosen, record.Version)
			for n := range ps {
				if ps[n].ID == chosen.ID {
					ps[n] = chosen
					ps[n].Available = false
				}
			}
			session = ""
			prompt += "\nPrevious provider unavailable. Continue existing worktree from handoff; preserve completed work.\n" + jsonText(t.Handoff)
			continue
		}
		return output, err
	}
	return provider.Output{}, errors.New("all capable providers exhausted")
}

// outcomes computes provider success rates for comparable roles and repository categories once at least five outcomes are available.
func (e *Engine) outcomes(ctx context.Context, role, category string) map[string]float64 {
	rs, _ := e.Store.List(ctx, "metrics")
	totals := map[string]float64{}
	wins := map[string]float64{}
	decisions := map[string]string{}
	outcomes, _ := e.Store.List(ctx, "outcomes")
	for _, r := range outcomes {
		var v struct {
			Task     string `json:"task"`
			Decision string `json:"human_decision"`
		}
		_ = json.Unmarshal(r.Data, &v)
		decisions[v.Task] = v.Decision
	}
	for _, r := range rs {
		var m struct {
			Provider string `json:"provider"`
			Role     string `json:"role"`
			Success  bool   `json:"success"`
			Category string `json:"category"`
			Task     string `json:"task"`
		}
		_ = json.Unmarshal(r.Data, &m)
		if m.Role != role || m.Category != category {
			continue
		}
		if role == "code" || role == "review" {
			record, x := e.Store.Get(ctx, "tasks", m.Task)
			if x != nil {
				continue
			}
			task, x := store.Decode[model.Task](record)
			if x != nil || (task.Stage != "pr_created" && task.Stage != "complete") {
				continue
			}
			m.Success = m.Success && task.ChecksPassed && task.Score >= 9 && decisions[m.Task] != "closed"
		}
		totals[m.Provider]++
		if m.Success {
			wins[m.Provider]++
		}
	}
	out := map[string]float64{}
	for p, n := range totals {
		if n >= 5 {
			out[p] = wins[p] / n
		}
	}
	return out
}

// index refreshes the project index against its current base commit and persists changed snapshots.
func (e *Engine) index(ctx context.Context, p model.Project) (ctxengine.Index, error) {
	r, err := e.Store.Get(ctx, "indexes", p.ID)
	var old *ctxengine.Index
	if err == nil {
		x, decode := store.Decode[ctxengine.Index](r)
		if decode != nil {
			return x, decode
		}
		old = &x
	}
	base, err := e.base(ctx, p)
	if err != nil {
		return ctxengine.Index{}, err
	}
	idx, err := ctxengine.Build(ctx, p.Path, old, base)
	if err != nil {
		return idx, err
	}
	if old == nil || idx.Commit != old.Commit {
		if err = e.Store.Put(ctx, "indexes", p.ID, idx, r.Version); err != nil && !errors.Is(err, store.ErrConflict) {
			return idx, err
		}
	}
	return idx, nil
}

// code restores or creates worktrees, runs dependency-ready units in parallel, integrates their commits serially, and verifies the combined task.
func (e *Engine) code(ctx context.Context, p model.Project, t *model.Task) error {
	if t.Worktree == "" {
		record, x := e.Store.Get(ctx, "workspaces", fmt.Sprintf("%s:%d", t.ID, t.PlanVersion))
		if x == nil {
			var saved struct{ Base, Branch, Path string }
			if json.Unmarshal(record.Data, &saved) == nil {
				t.Base = saved.Base
				t.Branch = saved.Branch
				t.Worktree = saved.Path
			}
		}
	}
	if t.Worktree == "" {
		base, err := e.base(ctx, p)
		if err != nil {
			return err
		}
		t.Base = base
		t.Branch = fmt.Sprintf("codex/whirlwind-%s-v%d", t.ID, t.PlanVersion)
		t.Worktree = filepath.Join(e.Root, "worktrees", t.ID, fmt.Sprintf("v%d", t.PlanVersion), "integration")
		if err = e.worktree(ctx, p.Path, t.Worktree, t.Branch, base); err != nil {
			return err
		}
	}
	workspaceKey := fmt.Sprintf("%s:%d", t.ID, t.PlanVersion)
	workspaceRecord, _ := e.Store.Get(ctx, "workspaces", workspaceKey)
	if workspaceRecord.Version == 0 {
		if err := e.Store.Put(ctx, "workspaces", workspaceKey, map[string]string{"Base": t.Base, "Branch": t.Branch, "Path": t.Worktree}, 0); err != nil {
			return err
		}
	}
	for n := range t.Plan.Units {
		u := &t.Plan.Units[n]
		record, x := e.Store.Get(ctx, "units", fmt.Sprintf("%s:%d:%s", t.ID, t.PlanVersion, u.ID))
		if x == nil {
			saved, _ := store.Decode[model.Unit](record)
			if saved.Stage == "integrated" {
				*u = saved
			}
		}
	}
	completed := map[string]bool{}
	for _, u := range t.Plan.Units {
		if u.Stage == "integrated" {
			completed[u.ID] = true
		}
	}
	for len(completed) < len(t.Plan.Units) {
		ready := []int{}
		for n, u := range t.Plan.Units {
			if completed[u.ID] {
				continue
			}
			ok := true
			for _, d := range u.Dependencies {
				if !completed[d] {
					ok = false
				}
			}
			if ok {
				ready = append(ready, n)
			}
		}
		if len(ready) == 0 {
			return errors.New("no runnable units")
		}
		limit := p.Policy.ProjectSlots
		if limit <= 0 {
			limit = e.settings(ctx).ProjectSlots
		}
		if len(ready) > limit {
			ready = ready[:limit]
		}
		planText := jsonText(t.Plan)
		snapshot := *t
		snapshot.Plan = nil
		var wg sync.WaitGroup
		errs := make(chan error, len(ready))
		for _, n := range ready {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				errs <- e.codeUnit(ctx, p, snapshot, &t.Plan.Units[n], planText)
			}(n)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				return err
			}
		}
		for _, n := range ready {
			u := &t.Plan.Units[n]
			head, err := github.Run(ctx, t.Worktree, "git", "rev-parse", "HEAD")
			if err != nil {
				return err
			}
			if head != u.Commit {
				ancestorErr := exec.CommandContext(ctx, "git", "-C", t.Worktree, "merge-base", "--is-ancestor", u.Commit, "HEAD").Run()
				if ancestorErr == nil {
					u.Stage = "integrated"
					completed[u.ID] = true
					continue
				}
				commits, x := github.Run(ctx, u.Worktree, "git", "rev-list", "--reverse", u.Base+".."+u.Commit)
				if x != nil {
					return x
				}
				if commits == "" {
					u.Stage = "integrated"
					completed[u.ID] = true
					continue
				}
				if _, err = github.Run(ctx, t.Worktree, "git", append([]string{"cherry-pick", "--keep-redundant-commits"}, strings.Fields(commits)...)...); err != nil {
					prompt := "Resolve cherry-pick conflict for implementation unit. Preserve both intended changes, finish cherry-pick using git cherry-pick --continue.\nUnit:\n" + jsonText(u)
					if _, x := e.agent(ctx, p, *t, "code", t.Worktree, prompt, false, ""); x != nil {
						return x
					}
					if _, x := github.Run(ctx, t.Worktree, "git", "rev-parse", "--verify", "CHERRY_PICK_HEAD"); x == nil {
						return errors.New("conflict unresolved")
					}
				}
			}
			u.Stage = "integrated"
			checkpointKey := fmt.Sprintf("%s:%d:%s", t.ID, t.PlanVersion, u.ID)
			checkpoint, _ := e.Store.Get(ctx, "units", checkpointKey)
			if err = e.Store.Put(ctx, "units", checkpointKey, *u, checkpoint.Version); err != nil {
				return err
			}
			completed[u.ID] = true
			t.Handoff.Commits = append(t.Handoff.Commits, u.Commit)
		}
	}
	t.Handoff.Worktree = t.Worktree
	t.Handoff.Branch = t.Branch
	t.Handoff.NextAction = "verify full implementation"
	if err := e.verify(ctx, p, t); err != nil {
		t.Stage = "repair"
		t.Error = err.Error()
		t.ChecksPassed = false
		return e.progress(ctx, t)
	}
	t.Handoff.CompletedWork = append(t.Handoff.CompletedWork, t.Plan.Objective)
	checkpointRecords, _ := e.Store.List(ctx, "checkpoints")
	for _, r := range checkpointRecords {
		var c struct {
			Task    string `json:"task"`
			Version int    `json:"plan_version"`
		}
		_ = json.Unmarshal(r.Data, &c)
		if c.Task == t.ID && c.Version == t.PlanVersion {
			t.Handoff.Checkpoints = append(t.Handoff.Checkpoints, r.ID)
		}
	}
	return nil
}

// codeUnit implements one isolated unit or recovers its completed checkpoint, preserving its lease and policy bounds.
func (e *Engine) codeUnit(ctx context.Context, p model.Project, t model.Task, u *model.Unit, planText string) error {
	branch := t.Branch + "-" + safe(u.ID)
	path := filepath.Join(filepath.Dir(t.Worktree), safe(u.ID))
	base, err := github.Run(ctx, t.Worktree, "git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	u.Branch = branch
	u.Worktree = path
	u.Base = base
	if err = e.worktree(ctx, p.Path, path, branch, base); err != nil {
		return err
	}
	ctx, cancelUnit := context.WithCancel(ctx)
	defer cancelUnit()
	leaseID := "unit:" + t.ID + ":" + u.ID
	lease, err := e.Store.Claim(ctx, leaseID, e.owner, time.Minute)
	if err != nil {
		return err
	}
	defer e.Store.Release(context.Background(), leaseID, e.owner, lease.Fence)
	e.renewLease(ctx, cancelUnit, leaseID, lease.Fence, nil)
	checkpointID := fmt.Sprintf("%s:%d:%s", t.ID, t.PlanVersion, u.ID)
	checkpoint, load := e.Store.Get(ctx, "units", checkpointID)
	if load == nil {
		previous, _ := store.Decode[model.Unit](checkpoint)
		if previous.Base != "" {
			u.Base = previous.Base
		}
		if previous.Commit != "" {
			*u = previous
			return nil
		}
	}
	if load != nil {
		u.Stage = "running"
		if err = e.Store.Put(ctx, "units", checkpointID, *u, 0); err != nil {
			return err
		}
	}
	unitPolicy := p
	if len(u.AllowedPaths) > 0 {
		for _, path := range u.AllowedPaths {
			if !policy.AllowedPath(p.Policy, path) {
				return errors.New("unit scope exceeds project policy")
			}
		}
		unitPolicy.Policy.AllowedPaths = u.AllowedPaths
	}
	prompt := "Implement only this unit, satisfy acceptance criteria, test changes. Do not push, create PR, change .whirlwind.yaml, or alter unrelated files.\nUnit:\n" + jsonText(u) + "\nPlan:\n" + planText + "\nHandoff:\n" + jsonText(t.Handoff)
	out, err := e.agent(ctx, unitPolicy, t, "code", path, prompt, false, "")
	if err != nil {
		return err
	}
	if err = e.commit(ctx, unitPolicy, path, u.Objective); err != nil {
		return err
	}
	u.Commit, err = github.Run(ctx, path, "git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	u.Stage = "complete"
	r, _ := e.Store.Get(ctx, "units", checkpointID)
	if err = e.Store.Put(ctx, "units", checkpointID, *u, r.Version); err != nil {
		return err
	}
	_ = e.Store.Put(ctx, "checkpoints", store.ID(), map[string]any{"task": t.ID, "unit": u.ID, "commit": u.Commit, "session": out.SessionID, "plan_version": t.PlanVersion}, 0)
	return nil
}

// safe strips unsafe characters from generated path/branch identifiers and substitutes a random ID if nothing remains.
func safe(id string) string {
	var b strings.Builder
	for _, c := range id {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || (c == '-' || c == '_') {
			b.WriteRune(c)
		}
	}
	s := b.String()
	if s == "" {
		s = store.ID()
	}
	return s
}

// worktree serializes Git worktree creation and reuses an existing worktree path.
func (e *Engine) worktree(ctx context.Context, repo, path, branch, base string) error {
	e.gitMu.Lock()
	defer e.gitMu.Unlock()
	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	_, err := github.Run(ctx, repo, "git", "worktree", "add", "-b", branch, path, base)
	return err
}

// commit rejects changed paths outside approved scope, stages changes, and commits nonempty diffs with Whirlwind identity.
func (e *Engine) commit(ctx context.Context, p model.Project, path, message string) error {
	files, err := github.Run(ctx, path, "git", "status", "--porcelain", "-uall")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(files, "\n") {
		if len(line) < 4 {
			continue
		}
		f := strings.TrimSpace(line[3:])
		if strings.Contains(f, " -> ") {
			f = strings.Split(f, " -> ")[1]
		}
		f = strings.Trim(f, "\"")
		if !policy.AllowedPath(p.Policy, f) {
			return fmt.Errorf("out-of-scope file: %s", f)
		}
	}
	if _, err = github.Run(ctx, path, "git", "add", "--all"); err != nil {
		return err
	}
	diff, err := github.Run(ctx, path, "git", "diff", "--cached", "--name-only")
	if err != nil || diff == "" {
		return err
	}
	_, err = github.Run(ctx, path, "git", "-c", "user.name=Whirlwind", "-c", "user.email=whirlwind@localhost", "commit", "-m", message)
	return err
}

// verify reserves capacity, checks diff scope, runs baseline and risk-specific checks, and stores diagnostics in the task handoff.
func (e *Engine) verify(ctx context.Context, p model.Project, t *model.Task) error {
	if err := e.acquire(ctx, p); err != nil {
		return err
	}
	defer e.release(p.ID)
	files, err := github.Run(ctx, t.Worktree, "git", "diff", "--name-only", t.Base+"...HEAD")
	if err != nil {
		return err
	}
	for _, file := range strings.Split(files, "\n") {
		if file != "" && !policy.AllowedPath(p.Policy, file) {
			return fmt.Errorf("scope check failed: %s", file)
		}
	}
	patch, err := github.Run(ctx, t.Worktree, "git", "diff", t.Base+"...HEAD")
	if err != nil {
		return err
	}
	risk, checks := verification.Select(p.Policy, strings.Split(files, "\n"), patch)
	t.Risk = risk
	checks = append(checks, model.Check{Name: "diff", Command: []string{"git", "diff", "--check", t.Base + "...HEAD"}}, model.Check{Name: "tracked-files-clean", Command: []string{"git", "diff", "--exit-code"}}, model.Check{Name: "index-clean", Command: []string{"git", "diff", "--cached", "--exit-code"}})
	report := verification.Run(ctx, t.Worktree, checks)
	_ = e.Store.Put(ctx, "verification", store.ID(), map[string]any{"task": t.ID, "plan_version": t.PlanVersion, "report": report, "risk": risk}, 0)
	t.Handoff.FailingTests = nil
	for _, r := range report.Results {
		if !r.Passed {
			t.Handoff.FailingTests = append(t.Handoff.FailingTests, r.Name+": "+r.Output+" "+r.Error)
		}
	}
	if !report.Passed {
		return errors.New(strings.Join(t.Handoff.FailingTests, "\n"))
	}
	return nil
}

// progress compares diff fingerprints, triggers recovery after five unchanged failures, and pauses after three unsuccessful recoveries.
func (e *Engine) progress(ctx context.Context, t *model.Task) error {
	diff, err := github.Run(ctx, t.Worktree, "git", "diff", t.Base)
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(diff))
	fingerprint := hex.EncodeToString(sum[:])
	if fingerprint == t.Fingerprint {
		t.UnchangedFailures++
	} else {
		t.UnchangedFailures = 1
		t.Fingerprint = fingerprint
	}
	if t.UnchangedFailures >= 5 {
		t.UnchangedFailures = 0
		t.Recoveries++
		t.Handoff.NextAction = "recover with new context or another provider"
		if t.Recoveries >= 3 {
			t.ResumeStage = t.Stage
			t.Stage = "paused"
			t.Error = "Three unsuccessful stall recoveries; user decision required"
			_ = e.Store.Notify(ctx, t.ID, "stall", t.Error)
		}
	}
	return nil
}

// Reconcile leases GitHub reconciliation, ingests eligible or withdrawn issues, and records terminal PR decisions.
func (e *Engine) Reconcile(ctx context.Context) {
	lease, err := e.Store.Claim(ctx, "reconcile", e.owner, 4*time.Minute)
	if err != nil {
		return
	}
	defer e.Store.Release(context.Background(), "reconcile", e.owner, lease.Fence)
	projects, _ := e.Store.List(ctx, "projects")
	for _, r := range projects {
		p, err := store.Decode[model.Project](r)
		if err != nil {
			continue
		}
		issues, err := e.GitHub.Issues(ctx, p.Repo)
		if err != nil {
			_ = e.Store.Notify(ctx, "", "github", err.Error())
			continue
		}
		seen := map[int]bool{}
		for _, i := range issues {
			seen[i.Number] = true
			e.Ingest(ctx, p, i)
		}
		tasks, _ := e.Store.List(ctx, "tasks")
		for _, r := range tasks {
			t, _ := store.Decode[model.Task](r)
			if t.ProjectID != p.ID {
				continue
			}
			if t.Issue.Number > 0 && !seen[t.Issue.Number] {
				i, err := e.GitHub.Issue(ctx, p.Repo, t.Issue.Number)
				if err == nil && t.Stage != "pr_created" && t.Stage != "complete" {
					e.Ingest(ctx, p, i)
				}
			}
			if t.PR != "" && t.Stage != "complete" {
				decision, err := e.GitHub.PRState(ctx, p.Repo, t.PR)
				if err == nil && (decision == "MERGED" || decision == "CLOSED") {
					t.Stage = "complete"
					if e.Store.Put(ctx, "tasks", t.ID, t, r.Version) != nil {
						continue
					}
					_ = e.Store.Put(ctx, "outcomes", store.ID(), map[string]any{"task": t.ID, "pr": t.PR, "human_decision": strings.ToLower(decision)}, 0)
				}
			}
		}
	}
}

// Ingest cancels tasks affected by issue changes before delegating deduplication and replanning to SQLite storage.
func (e *Engine) Ingest(ctx context.Context, p model.Project, i model.Issue) (model.Task, error) {
	rs, _ := e.Store.List(ctx, "tasks")
	for _, r := range rs {
		t, _ := store.Decode[model.Task](r)
		incoming, ix := time.Parse(time.RFC3339, i.UpdatedAt)
		existing, ex := time.Parse(time.RFC3339, t.Issue.UpdatedAt)
		if ix == nil && ex == nil && incoming.Before(existing) {
			continue
		}
		if t.EligibilityOverride && !i.Eligible() && t.Issue.Body == i.Body && t.Issue.Title == i.Title {
			continue
		}
		if t.ProjectID == p.ID && t.Issue.Number == i.Number && (!i.Eligible() || t.Issue.Body != i.Body || t.Issue.Title != i.Title || t.Issue.UpdatedAt != i.UpdatedAt) {
			e.Stop(t.ID)
		}
	}
	return e.Store.IngestIssue(ctx, p, i)
}

// Daily runs ideation for the latest due project/day, skipping completed schedules and delaying failed schedules until retry time.
func (e *Engine) Daily(ctx context.Context) {
	projects, _ := e.Store.List(ctx, "projects")
	day := DueDay(time.Now())
	for _, r := range projects {
		p, _ := store.Decode[model.Project](r)
		key := p.ID + ":" + day
		schedule, load := e.Store.Get(ctx, "schedules", key)
		if load == nil {
			var status struct {
				Stage   string `json:"stage"`
				RetryAt string `json:"retry_at"`
			}
			_ = json.Unmarshal(schedule.Data, &status)
			if status.Stage != "failed" {
				continue
			}
			retry, _ := time.Parse(time.RFC3339, status.RetryAt)
			if time.Now().Before(retry) {
				continue
			}
		}
		lease, err := e.Store.Claim(ctx, "ideation:"+key, e.owner, time.Hour)
		if err != nil {
			continue
		}
		err = e.Ideate(ctx, p, day)
		if err != nil {
			previous, _ := e.Store.Get(ctx, "schedules", key)
			_ = e.Store.Put(ctx, "schedules", key, map[string]any{"day": day, "project": p.ID, "stage": "failed", "retry_at": time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339), "error": err.Error()}, previous.Version)
			_ = e.Store.Notify(ctx, "", "ideation", p.Name+": "+err.Error())
		}
		_ = e.Store.Release(context.Background(), "ideation:"+key, e.owner, lease.Fence)
	}
}

// Ideate leases project generation, gathers indexed and GitHub context, requests four distinct directions, and saves them with schedule completion.
func (e *Engine) Ideate(ctx context.Context, p model.Project, day string) error {
	leaseID := "generation:" + p.ID
	lease, leaseErr := e.Store.Claim(ctx, leaseID, e.owner, time.Minute)
	if leaseErr != nil {
		return leaseErr
	}
	defer e.Store.Release(context.Background(), leaseID, e.owner, lease.Fence)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	e.renewLease(ctx, cancel, leaseID, lease.Fence, nil)

	idx, err := e.index(ctx, p)
	if err != nil {
		return err
	}
	sources, err := e.GitHub.Sources(ctx, p.Repo)
	if err != nil {
		return err
	}
	_ = e.Store.Put(ctx, "source_cache", store.ID(), map[string]any{"project": p.ID, "sources": sources}, 0)
	old, _ := e.Store.List(ctx, "directions")
	previous := []model.Direction{}
	for _, r := range old {
		d, _ := store.Decode[model.Direction](r)
		if d.ProjectID == p.ID {
			previous = append(previous, d)
		}
	}
	summary := []map[string]any{}
	for _, f := range idx.Files {
		summary = append(summary, map[string]any{"path": f.Path, "symbols": f.Symbols, "todos": f.TODOs})
	}
	prompt := "Generate exactly four new, distinct, useful innovation directions. Use supplied index only; do not inspect full repository. Return JSON array [{title,description}].\nRepository index:\n" + jsonText(summary) + "\nGitHub sources:\n" + jsonText(sources) + "\nPrevious suggestions:\n" + jsonText(previous)
	scratch := filepath.Join(e.Root, "ideation", p.ID)
	if err = os.MkdirAll(scratch, 0700); err != nil {
		return err
	}
	out, err := e.agent(ctx, p, model.Task{ID: "ideation:" + p.ID, Issue: model.Issue{Title: "innovation"}}, "ideate", scratch, prompt, true, "")
	if err != nil {
		return err
	}
	var directions []model.Direction
	if err = provider.JSON(out.Text, &directions); err != nil {
		return err
	}
	if len(directions) != 4 {
		return errors.New("ideation must produce four directions")
	}
	seen := map[string]bool{}
	for _, d := range previous {
		seen[strings.ToLower(strings.TrimSpace(d.Title))] = true
	}
	for _, d := range directions {
		key := strings.ToLower(strings.TrimSpace(d.Title))
		if key == "" || d.Description == "" || seen[key] {
			return errors.New("duplicate or incomplete direction; retry ideation")
		}
		seen[key] = true
	}
	for n := range directions {
		d := &directions[n]
		d.ID = store.ID()
		d.ProjectID = p.ID
		d.Day = day
	}
	return e.Store.SaveDirections(ctx, p.ID, day, directions)
}

type Settings struct {
	GlobalSlots           int `json:"global_slots"`
	ProjectSlots          int `json:"project_slots"`
	ReconciliationSeconds int `json:"reconciliation_seconds"`
}

// settings loads global worker settings over the default four global slots, two project slots, and five-minute reconciliation interval.
func (e *Engine) settings(ctx context.Context) Settings {
	v := Settings{4, 2, 300}
	if r, err := e.Store.Get(ctx, "settings", "global"); err == nil {
		_ = json.Unmarshal(r.Data, &v)
	}
	return v
}

// acquire waits for both global and per-project execution capacity, or returns on cancellation.
func (e *Engine) acquire(ctx context.Context, p model.Project) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		settings := e.settings(ctx)
		global := settings.GlobalSlots
		project := settings.ProjectSlots
		if p.Policy.GlobalSlots > 0 && p.Policy.GlobalSlots < global {
			global = p.Policy.GlobalSlots
		}
		if p.Policy.ProjectSlots > 0 {
			project = p.Policy.ProjectSlots
		}
		e.mu.Lock()
		total := 0
		for _, n := range e.active {
			total += n
		}
		if total < global && e.active[p.ID] < project {
			e.active[p.ID]++
			e.mu.Unlock()
			return nil
		}
		e.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// release returns one reserved execution slot to the project capacity pool.
func (e *Engine) release(project string) { e.mu.Lock(); e.active[project]--; e.mu.Unlock() }

// base fetches and resolves the configured remote default branch, or uses local HEAD when no default branch is recorded.
func (e *Engine) base(ctx context.Context, p model.Project) (string, error) {
	if p.DefaultBranch == "" {
		return github.Run(ctx, p.Path, "git", "rev-parse", "HEAD")
	}
	e.gitMu.Lock()
	defer e.gitMu.Unlock()
	if _, err := github.Run(ctx, p.Path, "git", "fetch", "--no-tags", "origin", p.DefaultBranch); err != nil {
		return "", err
	}
	return github.Run(ctx, p.Path, "git", "rev-parse", "refs/remotes/origin/"+p.DefaultBranch)
}

// resumeSession finds the latest stored session matching task, role, provider, worktree, and plan version.
func (e *Engine) resumeSession(ctx context.Context, task, role, providerID, cwd string, version int) string {
	rs, _ := e.Store.List(ctx, "sessions")
	last := ""
	for _, r := range rs {
		var v struct {
			Task     string `json:"task"`
			Role     string `json:"role"`
			Provider string `json:"provider"`
			CWD      string `json:"cwd"`
			Version  int    `json:"plan_version"`
			Session  string `json:"session_id"`
		}
		_ = json.Unmarshal(r.Data, &v)
		if v.Task == task && v.Role == role && v.Provider == providerID && v.CWD == cwd && v.Version == version {
			last = v.Session
		}
	}
	return last
}
