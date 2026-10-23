package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/prompts/agents"
)

type discoverySnapshot struct {
	Fingerprint   string              `json:"fingerprint"`
	Commit        string              `json:"commit"`
	Files         map[string]string   `json:"files"`
	Sources       string              `json:"sources"`
	Brief         model.IdeationBrief `json:"brief"`
	Evidence      []model.EvidenceRef `json:"evidence"`
	Diagnostics   map[string]string   `json:"diagnostics"`
	PolicyHash    string              `json:"policy_hash"`
	MaxCandidates int                 `json:"max_candidates"`
}

type freshDiscoveryKey struct{}

func WithFreshDiscovery(ctx context.Context, fresh bool) context.Context {
	return context.WithValue(ctx, freshDiscoveryKey{}, fresh)
}

func (e *Engine) prepareDiscovery(ctx context.Context, p model.Project) (string, []model.Direction, discoverySnapshot, bool, error) {
	index, err := e.index(ctx, p)
	if err != nil {
		return "", nil, discoverySnapshot{}, false, err
	}
	var sources any
	var externalRefs []model.EvidenceRef
	if e.Agentic(ctx) {
		sources, externalRefs, err = e.integrationContext(ctx, p)
	} else {
		sources, err = e.GitHub.Sources(ctx, p.Repo)
	}
	if err != nil {
		return "", nil, discoverySnapshot{}, false, err
	}
	var previous discoverySnapshot
	record, err := e.Store.Get(ctx, "discovery_scans", p.ID)
	if err == nil {
		previous, err = store.Decode[discoverySnapshot](record)
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", nil, previous, false, err
	}
	brief := model.IdeationBrief{}
	if r, load := e.Store.Get(ctx, "ideation_briefs", p.ID); load == nil {
		brief, err = store.Decode[model.IdeationBrief](r)
		if err != nil {
			return "", nil, previous, false, err
		}
	} else if !errors.Is(load, store.ErrNotFound) {
		return "", nil, previous, false, load
	}
	snapshot := discoverySnapshot{Commit: index.Commit, Files: map[string]string{}, Sources: jsonText(sources), Brief: brief}
	snapshot.Evidence = append(snapshot.Evidence, externalRefs...)
	snapshot.PolicyHash = p.PolicyHash
	snapshot.MaxCandidates = brief.MaxCandidates
	if snapshot.MaxCandidates == 0 {
		snapshot.MaxCandidates = e.settings(ctx).Ideation.MaxCandidates
	}
	if snapshot.MaxCandidates == 0 {
		snapshot.MaxCandidates = 4
	}
	diagnostics, refs, err := e.discoveryDiagnostics(ctx, p.ID)
	if err != nil {
		return "", nil, snapshot, false, err
	}
	snapshot.Diagnostics = diagnostics
	snapshot.Evidence = append(snapshot.Evidence, refs...)
	if p.PolicyHash != "" {
		snapshot.Evidence = append(snapshot.Evidence, model.EvidenceRef{Kind: "policy", Ref: p.ID, Revision: p.PolicyHash, Summary: "Confirmed project execution policy"})
	}
	changed := []map[string]any{}
	paths := make([]string, 0, len(index.Files))
	for path := range index.Files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		entry := index.Files[path]
		fingerprint := model.Fingerprint([]byte(entry.Text))
		snapshot.Files[path] = fingerprint
		if previous.Files[path] != fingerprint {
			snapshot.Evidence = append(snapshot.Evidence, model.EvidenceRef{Kind: "code", Ref: path, Revision: index.Commit, Summary: "Changed indexed source"})
			if len(changed) < 24 {
				changed = append(changed, map[string]any{"path": path, "symbols": entry.Symbols, "todos": entry.TODOs, "action_markers": entry.ActionMarkers})
			}
		}
	}
	for path := range previous.Files {
		if _, exists := snapshot.Files[path]; !exists {
			snapshot.Evidence = append(snapshot.Evidence, model.EvidenceRef{Kind: "code", Ref: path, Revision: index.Commit, Summary: "Removed indexed source"})
		}
	}
	if previous.Sources != snapshot.Sources && !e.Agentic(ctx) {
		snapshot.Evidence = append(snapshot.Evidence, model.EvidenceRef{Kind: "github", Ref: p.Repo, Revision: model.Fingerprint([]byte(snapshot.Sources)), Summary: "Issue and discussion snapshot"})
	}
	snapshot.Fingerprint = model.Fingerprint([]byte(jsonText(struct {
		Commit        string
		Files         map[string]string
		Sources       string
		Brief         model.IdeationBrief
		Diagnostics   map[string]string
		PolicyHash    string
		MaxCandidates int
	}{snapshot.Commit, snapshot.Files, stableIntegrationContext(snapshot.Sources), brief, snapshot.Diagnostics, snapshot.PolicyHash, snapshot.MaxCandidates})))
	fresh, _ := ctx.Value(freshDiscoveryKey{}).(bool)
	if previous.Fingerprint == snapshot.Fingerprint && !fresh {
		return "", nil, snapshot, true, nil
	}
	if fresh && len(changed) == 0 {
		for _, path := range paths[:min(len(paths), 24)] {
			entry := index.Files[path]
			changed = append(changed, map[string]any{"path": path, "symbols": entry.Symbols, "todos": entry.TODOs, "action_markers": entry.ActionMarkers})
			snapshot.Evidence = append(snapshot.Evidence, model.EvidenceRef{Kind: "code", Ref: path, Revision: index.Commit, Summary: "Explicit fresh exploration"})
		}
	}
	history, err := e.previousDirections(ctx, p.ID)
	if err != nil {
		return "", nil, snapshot, false, err
	}
	history = relevantDirections(history, changed, brief)
	if len(snapshot.Evidence) == 0 {
		snapshot.Evidence = []model.EvidenceRef{{Kind: "repository", Ref: p.Repo, Revision: index.Commit, Summary: "Current repository snapshot"}}
	}
	prompt, err := agents.Render("ideate.md", map[string]string{"Index": boundedSources(jsonText(changed)), "Sources": boundedSources(snapshot.Sources), "Previous": boundedSources(jsonText(history)), "Brief": jsonText(brief), "Evidence": boundedSources(jsonText(snapshot.Evidence)), "Diagnostics": boundedSources(jsonText(snapshot.Diagnostics)), "MaxCandidates": strconv.Itoa(snapshot.MaxCandidates)})
	return prompt, history, snapshot, false, err
}

func boundedSources(sources string) string {
	// Keep valid source JSON available in the persisted snapshot; bound model context only.
	if len(sources) > 24000 {
		end := 24000
		for end > 0 && !utf8.RuneStart(sources[end]) {
			end--
		}
		return sources[:end] + "\n[Source excerpt truncated; do not infer missing evidence]"
	}
	return sources
}

func relevantDirections(history []model.Direction, changed []map[string]any, brief model.IdeationBrief) []model.Direction {
	query := strings.ToLower(brief.Goal + " " + brief.Constraints + " " + brief.Priorities + " " + jsonText(changed))
	sort.SliceStable(history, func(i, j int) bool { return relevance(history[i], query) > relevance(history[j], query) })
	if len(history) > 20 {
		history = history[:20]
	}
	return history
}

func (e *Engine) discoveryDiagnostics(ctx context.Context, project string) (map[string]string, []model.EvidenceRef, error) {
	tasks, err := e.Store.List(ctx, "tasks")
	if err != nil {
		return nil, nil, err
	}
	owned := map[string]bool{}
	for _, record := range tasks {
		task, decode := store.Decode[model.Task](record)
		if decode != nil {
			return nil, nil, decode
		}
		if task.ProjectID == project {
			owned[task.ID] = true
		}
	}
	records, err := e.Store.List(ctx, "verification")
	if err != nil {
		return nil, nil, err
	}
	type check struct {
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
			Passed  bool    `json:"passed"`
			Results []check `json:"results"`
		} `json:"report"`
	}
	latest := map[string]struct {
		record model.Record
		value  verificationEvidence
	}{}
	for _, record := range records {
		var value verificationEvidence
		if err = json.Unmarshal(record.Data, &value); err != nil {
			return nil, nil, err
		}
		if value.Task == "" {
			value.Task = value.Provenance.Task
		}
		if owned[value.Task] {
			latest[value.Task] = struct {
				record model.Record
				value  verificationEvidence
			}{record, value}
		}
	}
	ids := make([]string, 0, len(latest))
	for id := range latest {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	diagnostics := map[string]string{}
	refs := []model.EvidenceRef{}
	for _, id := range ids[:min(len(ids), 20)] {
		entry := latest[id]
		failed := false
		for _, result := range entry.value.Report.Results {
			if !result.Passed {
				key := id + ":" + result.Name
				diagnostics[key] = boundedSources(result.Output + " " + result.Error)
				failed = true
			}
		}
		if entry.value.Error != "" {
			diagnostics[id+":policy_or_verification"] = boundedSources(entry.value.Error)
			failed = true
		}
		if failed {
			refs = append(refs, model.EvidenceRef{Kind: "verification", Ref: entry.record.ID, Revision: model.Fingerprint([]byte(jsonText(entry.value.Report))), Summary: "Persisted failed checks; checks were not rerun for discovery"})
		}
	}
	return diagnostics, refs, nil
}

func relevance(direction model.Direction, query string) int {
	score := 0
	for _, word := range strings.Fields(directionKey(direction.Title + " " + direction.Description)) {
		if len(word) > 3 && strings.Contains(query, word) {
			score++
		}
	}
	return score
}

func validateCandidateEvidence(directions []model.Direction, snapshot discoverySnapshot) error {
	available := map[string]model.EvidenceRef{}
	for _, ref := range snapshot.Evidence {
		available[ref.Kind+":"+ref.Ref] = ref
	}
	for n := range directions {
		candidate := directions[n]
		if strings.TrimSpace(candidate.ExpectedBenefit) == "" || strings.TrimSpace(candidate.EffortEstimate) == "" {
			return errors.New("candidate must state expected_benefit and effort_estimate")
		}
		if candidate.ProblemKind != "observed_defect" && candidate.ProblemKind != "opportunity" {
			return errors.New("candidate problem_kind must distinguish observed_defect from opportunity")
		}
		if candidate.ProblemKind == "opportunity" && len(candidate.Uncertainty) == 0 {
			return errors.New("an opportunity must state uncertainty and validation still needed")
		}
		if len(candidate.ExpectedBenefit) > 2000 || len(candidate.EffortEstimate) > 500 || len(candidate.Uncertainty) > 10 {
			return errors.New("candidate assessment exceeds bounded metadata size")
		}
		for _, uncertainty := range candidate.Uncertainty {
			if strings.TrimSpace(uncertainty) == "" || len(uncertainty) > 1000 {
				return errors.New("candidate uncertainty must be concise nonempty text")
			}
		}
		if len(directions[n].Evidence) == 0 {
			return errors.New("candidate must cite supplied evidence")
		}
		canonical := []model.EvidenceRef{}
		directFailure := false
		for _, ref := range directions[n].Evidence {
			supplied, ok := available[ref.Kind+":"+ref.Ref]
			if !ok {
				return errors.New("candidate cites evidence outside the supplied snapshot")
			}
			canonical = append(canonical, supplied)
			if supplied.Kind == "verification" {
				directFailure = true
			}
		}
		if candidate.ProblemKind == "observed_defect" && !directFailure {
			return errors.New("observed_defect requires persisted verification failure evidence; classify unverified source hypotheses as opportunity")
		}
		sort.Slice(canonical, func(i, j int) bool {
			return canonical[i].Kind+":"+canonical[i].Ref < canonical[j].Kind+":"+canonical[j].Ref
		})
		directions[n].Evidence = canonical
		identity := append([]model.EvidenceRef{}, canonical...)
		for i := range identity {
			identity[i].RetrievedAt, identity[i].AttemptID = "", ""
		}
		directions[n].Fingerprint = model.Fingerprint([]byte(jsonText(struct {
			Title, Description string
			Evidence           []model.EvidenceRef
		}{directionKey(directions[n].Title), directionKey(directions[n].Description), identity})))
		directions[n].Status = "proposed"
	}
	return nil
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
