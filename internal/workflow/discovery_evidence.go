package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	ctxengine "whirlwind/internal/context"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/prompts/agents"
)

type discoverySnapshot struct {
	HistoryContextBytes   int                             `json:"-"`
	PlanContextBytes      int                             `json:"-"`
	Fingerprint           string                          `json:"fingerprint"`
	Commit                string                          `json:"commit"`
	Files                 map[string]string               `json:"files"`
	Sources               string                          `json:"sources"`
	ApprovedPlans         []approvedPlanContext           `json:"approved_plans,omitempty"`
	HumanReconsiderations []model.ProposalReconsideration `json:"human_reconsiderations,omitempty"`
	ExplorationAreas      []string                        `json:"exploration_areas,omitempty"`
	CurrentRepository     []map[string]any                `json:"current_repository,omitempty"`
	Brief                 model.IdeationBrief             `json:"brief"`
	Evidence              []model.EvidenceRef             `json:"evidence"`
	Diagnostics           map[string]string               `json:"diagnostics"`
	PolicyHash            string                          `json:"policy_hash"`
	MaxCandidates         int                             `json:"max_candidates"`
}

type approvedPlanContext struct {
	TaskID      string   `json:"task"`
	PlanVersion int      `json:"version"`
	Hash        string   `json:"approved_hash"`
	Objective   string   `json:"objective"`
	Outcomes    []string `json:"outcomes,omitempty"`
	Scope       []string `json:"scope,omitempty"`
}

type discoveryBase struct {
	index          ctxengine.Index
	sources        any
	externalRefs   []model.EvidenceRef
	previous       discoverySnapshot
	brief          model.IdeationBrief
	diagnostics    map[string]string
	diagnosticRefs []model.EvidenceRef
}

type verificationCheck struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Output string `json:"output"`
	Error  string `json:"error"`
}

type verificationEvidence struct {
	Task       string           `json:"task"`
	Provenance model.Provenance `json:"provenance"`
	Error      string           `json:"error"`
	Report     struct {
		Passed  bool                `json:"passed"`
		Results []verificationCheck `json:"results"`
	} `json:"report"`
}

type latestVerification struct {
	record model.Record
	value  verificationEvidence
}

type freshDiscoveryKey struct{}

func WithFreshDiscovery(ctx context.Context, fresh bool) context.Context {
	return context.WithValue(ctx, freshDiscoveryKey{}, fresh)
}

func (e *Engine) prepareDiscovery(ctx context.Context, p model.Project) (string, []model.Direction, discoverySnapshot, bool, error) {
	base, err := e.loadDiscoveryBase(ctx, p)
	if err != nil {
		return "", nil, base.previous, false, err
	}
	index, previous, brief := base.index, base.previous, base.brief
	snapshot := discoverySnapshot{Commit: index.Commit, Files: map[string]string{}, Sources: jsonText(base.sources), Brief: brief}
	snapshot.Evidence = append(snapshot.Evidence, base.externalRefs...)
	snapshot.PolicyHash = p.PolicyHash
	configuredMax := brief.MaxCandidates
	if configuredMax == 0 {
		configuredMax = e.settings(ctx).Ideation.MaxCandidates
	}
	if configuredMax != 0 && configuredMax != 4 {
		return "", nil, snapshot, false, fmt.Errorf("ideation requires exactly four directions (one per category); configured maximum %d is incompatible, update the saved ideation configuration", configuredMax)
	}
	snapshot.MaxCandidates = 4
	snapshot.Diagnostics = base.diagnostics
	snapshot.Evidence = append(snapshot.Evidence, base.diagnosticRefs...)
	if p.PolicyHash != "" {
		snapshot.Evidence = append(snapshot.Evidence, model.EvidenceRef{Kind: "policy", Ref: p.ID, Revision: p.PolicyHash, Summary: "Confirmed project execution policy"})
	}
	changed, paths := indexedDiscoveryChanges(index, previous, &snapshot)
	snapshot.HumanReconsiderations, err = e.Store.PendingProposalReconsiderations(ctx, p.ID, 12)
	if err != nil {
		return "", nil, snapshot, false, err
	}
	reconsiderationBytes := len(boundedSources(jsonText(snapshot.HumanReconsiderations)))
	componentCounts, err := e.Store.ProposalComponentCounts(ctx, p.ID)
	if err != nil {
		return "", nil, snapshot, false, err
	}
	snapshot.ExplorationAreas = rotatingDiscoveryAreas(paths, componentCounts, 12)
	if previous.Sources != snapshot.Sources && !e.Agentic(ctx) {
		snapshot.Evidence = append(snapshot.Evidence, model.EvidenceRef{Kind: "github", Ref: p.Repo, Revision: model.Fingerprint([]byte(snapshot.Sources)), Summary: "Issue and discussion snapshot"})
	}
	fresh, _ := ctx.Value(freshDiscoveryKey{}).(bool)
	changed = addFreshDiscoveryContext(fresh, changed, paths, index, &snapshot)
	snapshot.CurrentRepository = changed
	historyTerms := strings.Join([]string{brief.Goal, brief.Constraints, brief.Priorities, jsonText(changed), jsonText(snapshot.Diagnostics), jsonText(snapshot.Evidence)}, " ")
	historyBudget, planBudget := discoveryHistoryBudgets(e.settings(ctx).Ideation)
	if reconsiderationBytes > historyBudget {
		return "", nil, snapshot, false, errors.New("ideation history budget cannot fit all pending human reconsideration requests")
	}
	history, err := e.searchPreviousDirections(ctx, p.ID, historyTerms, historyBudget-reconsiderationBytes)
	if err != nil {
		return "", nil, snapshot, false, err
	}
	history = relevantDirections(history, changed, brief)
	snapshot.ApprovedPlans, err = e.currentApprovedPlans(ctx, p.ID, historyTerms, planBudget, 12)
	if err != nil {
		return "", nil, snapshot, false, err
	}
	snapshot.HistoryContextBytes = len(boundedSources(jsonText(compactProposalHistory(history)))) + reconsiderationBytes
	snapshot.PlanContextBytes = len(boundedSources(jsonText(snapshot.ApprovedPlans)))
	snapshot.Fingerprint = discoveryFingerprint(snapshot)
	if previous.Fingerprint == snapshot.Fingerprint && !fresh {
		return "", nil, snapshot, true, nil
	}
	if len(snapshot.Evidence) == 0 {
		snapshot.Evidence = []model.EvidenceRef{{Kind: "repository", Ref: p.Repo, Revision: index.Commit, Summary: "Current repository snapshot"}}
	}
	prompt, err := agents.Render("ideate.md", map[string]string{"Index": boundedSources(jsonText(changed)), "Sources": boundedSources(snapshot.Sources), "Previous": boundedSources(jsonText(compactProposalHistory(history))), "ApprovedPlans": boundedSources(jsonText(snapshot.ApprovedPlans)), "HumanReconsiderations": boundedSources(jsonText(snapshot.HumanReconsiderations)), "Areas": jsonText(snapshot.ExplorationAreas), "Brief": jsonText(brief), "Evidence": boundedSources(jsonText(snapshot.Evidence)), "Diagnostics": boundedSources(jsonText(snapshot.Diagnostics)), "MaxCandidates": strconv.Itoa(snapshot.MaxCandidates)})
	return prompt, history, snapshot, false, err
}

func (e *Engine) loadDiscoveryBase(ctx context.Context, project model.Project) (discoveryBase, error) {
	base := discoveryBase{brief: model.IdeationBrief{}}
	index, err := e.index(ctx, project)
	if err != nil {
		return base, err
	}
	base.index = index
	if err := e.loadDiscoverySources(ctx, project, &base); err != nil {
		return base, err
	}
	if err := e.loadDiscoveryRecords(ctx, project.ID, &base); err != nil {
		return base, err
	}
	base.diagnostics, base.diagnosticRefs, err = e.discoveryDiagnostics(ctx, project.ID)
	return base, err
}

func (e *Engine) loadDiscoverySources(ctx context.Context, project model.Project, base *discoveryBase) error {
	var err error
	if e.Agentic(ctx) {
		base.sources, base.externalRefs, err = e.integrationContext(ctx, project, base.index.Commit)
	} else {
		base.sources, err = e.GitHub.Sources(ctx, project.Repo)
	}
	return err
}

func (e *Engine) loadDiscoveryRecords(ctx context.Context, projectID string, base *discoveryBase) error {
	if record, err := e.Store.Get(ctx, "discovery_scans", projectID); err == nil {
		base.previous, err = store.Decode[discoverySnapshot](record)
		if err != nil {
			return err
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if record, err := e.Store.Get(ctx, "ideation_briefs", projectID); err == nil {
		base.brief, err = store.Decode[model.IdeationBrief](record)
		if err != nil {
			return err
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	return nil
}

func (e *Engine) currentApprovedPlans(ctx context.Context, projectID, terms string, maxBytes, limit int) ([]approvedPlanContext, error) {
	if maxBytes <= 0 || limit <= 0 {
		return nil, nil
	}
	tasks, err := e.Store.List(ctx, "tasks")
	if err != nil {
		return nil, err
	}
	plans := make([]approvedPlanContext, 0, min(len(tasks), limit))
	for _, record := range tasks {
		task, err := store.Decode[model.Task](record)
		if err != nil {
			return nil, err
		}
		if task.ProjectID != projectID || task.PlanDocument == nil || task.PlanDocument.ApprovedHash == "" || task.PlanDocument.ApprovedHash != task.PlanDocument.Hash {
			continue
		}
		version := task.PlanVersion
		if task.PlanDocument.PlanVersion > 0 {
			if task.PlanDocument.PlanVersion != version {
				continue
			}
			version = task.PlanDocument.PlanVersion
		}
		if version < 1 {
			continue
		}
		approved, err := e.Store.Get(ctx, "plans", fmt.Sprintf("%s:%d", task.ID, version))
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		plan, err := store.Decode[model.Plan](approved)
		if err != nil {
			return nil, err
		}
		plans = append(plans, approvedPlanContext{TaskID: task.ID, PlanVersion: version, Hash: task.PlanDocument.ApprovedHash, Objective: plan.Objective, Outcomes: approvedPlanOutcomes(plan), Scope: approvedPlanScope(plan)})
	}
	sort.SliceStable(plans, func(i, j int) bool {
		left, right := approvedPlanRelevance(plans[i], terms), approvedPlanRelevance(plans[j], terms)
		if left != right {
			return left > right
		}
		return plans[i].TaskID < plans[j].TaskID
	})
	bounded := make([]approvedPlanContext, 0, min(len(plans), limit))
	for _, plan := range plans {
		if len(bounded) >= limit {
			break
		}
		candidate := append(append([]approvedPlanContext{}, bounded...), plan)
		if len(jsonText(candidate)) > maxBytes {
			continue
		}
		bounded = append(bounded, plan)
	}
	return bounded, nil
}

func approvedPlanOutcomes(plan model.Plan) []string {
	outcomes := append([]string{}, plan.AcceptanceCriteria...)
	for _, requirement := range plan.Requirements {
		if outcome := strings.TrimSpace(requirement.Outcome); outcome != "" {
			outcomes = append(outcomes, outcome)
		}
	}
	return outcomes
}

func approvedPlanScope(plan model.Plan) []string {
	scope := append([]string{}, plan.Where...)
	for _, unit := range plan.Units {
		scope = append(scope, unit.AllowedPaths...)
	}
	slices.Sort(scope)
	return slices.Compact(scope)
}

func approvedPlanRelevance(plan approvedPlanContext, terms string) int {
	content := strings.ToLower(jsonText(plan))
	count := 0
	seen := map[string]bool{}
	for _, term := range strings.Fields(strings.ToLower(terms)) {
		term = strings.Trim(term, "\"'.,:;!?()[]{}")
		if len(term) < 3 || seen[term] {
			continue
		}
		seen[term] = true
		if strings.Contains(content, term) {
			count++
		}
	}
	return count
}

func rotatingDiscoveryAreas(paths []string, counts map[string]int, limit int) []string {
	areas := append([]string{}, paths...)
	sort.SliceStable(areas, func(i, j int) bool {
		left, right := counts[areas[i]], counts[areas[j]]
		if left != right {
			return left < right
		}
		return areas[i] < areas[j]
	})
	if limit <= 0 || limit > 12 {
		limit = 12
	}
	if len(areas) > limit {
		areas = areas[:limit]
	}
	return areas
}

// Audit capture metadata stays in the persisted source snapshot. Only its
// stable semantic projection participates in deduplication; nested source
// content and all native inventory fields remain part of that identity.
func stableIntegrationContext(sources string) string {
	var context map[string]json.RawMessage
	if json.Unmarshal([]byte(sources), &context) != nil || context["inventory"] == nil || context["sources"] == nil {
		return sources
	}
	delete(context, "attempt_id")
	delete(context, "retrieved_at")
	return jsonText(context)
}

func (e *Engine) saveDiscoverySnapshot(ctx context.Context, project string, snapshot discoverySnapshot) error {
	record, err := e.Store.Get(ctx, "discovery_scans", project)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	return e.Store.Put(ctx, "discovery_scans", project, snapshot, record.Version)
}

func unchangedCandidateEvidence(history []model.Direction, evidence []model.EvidenceRef) []model.Direction {
	current := map[string]string{}
	for _, ref := range evidence {
		current[ref.Kind+":"+ref.Ref] = ref.Revision
	}
	unchanged := []model.Direction{}
	for _, candidate := range history {
		if len(candidate.Evidence) == 0 {
			unchanged = append(unchanged, candidate)
			continue
		}
		for _, ref := range candidate.Evidence {
			if revision, exists := current[ref.Kind+":"+ref.Ref]; !exists || revision == ref.Revision {
				unchanged = append(unchanged, candidate)
				break
			}
		}
	}
	return unchanged
}
