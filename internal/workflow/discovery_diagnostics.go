package workflow

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (e *Engine) discoveryDiagnostics(ctx context.Context, project string) (map[string]string, []model.EvidenceRef, error) {
	tasks, err := e.Store.List(ctx, "tasks")
	if err != nil {
		return nil, nil, err
	}
	owned, err := discoveryTaskIDs(tasks, project)
	if err != nil {
		return nil, nil, err
	}
	records, err := e.Store.List(ctx, "verification")
	if err != nil {
		return nil, nil, err
	}
	latest, err := latestVerifications(records, owned)
	if err != nil {
		return nil, nil, err
	}
	return diagnosticsForLatest(latest)
}

func discoveryTaskIDs(records []model.Record, project string) (map[string]bool, error) {
	owned := map[string]bool{}
	for _, record := range records {
		task, err := store.Decode[model.Task](record)
		if err != nil {
			return nil, err
		}
		if task.ProjectID == project {
			owned[task.ID] = true
		}
	}
	return owned, nil
}

func latestVerifications(records []model.Record, owned map[string]bool) (map[string]latestVerification, error) {
	latest := map[string]latestVerification{}
	for _, record := range records {
		var value verificationEvidence
		if err := json.Unmarshal(record.Data, &value); err != nil {
			return nil, err
		}
		if value.Task == "" {
			value.Task = value.Provenance.Task
		}
		if owned[value.Task] {
			latest[value.Task] = latestVerification{record, value}
		}
	}
	return latest, nil
}

func diagnosticsForLatest(latest map[string]latestVerification) (map[string]string, []model.EvidenceRef, error) {
	ids := make([]string, 0, len(latest))
	for id := range latest {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	diagnostics := map[string]string{}
	refs := []model.EvidenceRef{}
	for _, id := range ids[:min(len(ids), 20)] {
		entry := latest[id]
		if appendVerificationDiagnostics(id, entry, diagnostics) {
			refs = append(refs, model.EvidenceRef{Kind: "verification", Ref: entry.record.ID, Revision: model.Fingerprint([]byte(jsonText(entry.value.Report))), Summary: "Persisted failed checks; checks were not rerun for discovery"})
		}
	}
	return diagnostics, refs, nil
}

func appendVerificationDiagnostics(id string, entry latestVerification, diagnostics map[string]string) bool {
	failed := false
	for _, result := range entry.value.Report.Results {
		if result.Passed {
			continue
		}
		diagnostics[id+":"+result.Name] = boundedSources(result.Output + " " + result.Error)
		failed = true
	}
	if entry.value.Error != "" {
		diagnostics[id+":policy_or_verification"] = boundedSources(entry.value.Error)
		failed = true
	}
	return failed
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
