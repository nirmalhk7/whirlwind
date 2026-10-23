package evaluation

import (
	"context"
	"encoding/json"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

// Load reads durable evidence only. It never starts inference or runs checks.
func Load(ctx context.Context, s *store.Store) (Report, error) {
	tasks, err := s.List(ctx, "tasks")
	if err != nil {
		return Report{}, err
	}
	attempts, err := s.List(ctx, "sessions")
	if err != nil {
		return Report{}, err
	}
	outcomes, err := s.List(ctx, "outcomes")
	if err != nil {
		return Report{}, err
	}
	metrics, err := s.List(ctx, "metrics")
	if err != nil {
		return Report{}, err
	}
	audits, err := s.List(ctx, "audit")
	if err != nil {
		return Report{}, err
	}
	decisions := map[string]string{}
	decisionHistory := map[string][]string{}
	for _, r := range outcomes {
		var v struct {
			Task     string `json:"task"`
			Decision string `json:"human_decision"`
		}
		if err = json.Unmarshal(r.Data, &v); err != nil {
			return Report{}, err
		}
		decisions[v.Task] = v.Decision
		decisionHistory[v.Task] = append(decisionHistory[v.Task], v.Decision)
	}
	categories := map[string]string{}
	for _, r := range metrics {
		var v struct {
			Task     string `json:"task"`
			Category string `json:"category"`
		}
		if err = json.Unmarshal(r.Data, &v); err != nil {
			return Report{}, err
		}
		categories[v.Task] = v.Category
	}
	interventions := map[string]int{}
	for _, r := range audits {
		a, e := store.Decode[model.Audit](r)
		if e != nil {
			return Report{}, e
		}
		if a.Source == "local_operator" {
			interventions[a.Task]++
		}
	}
	byTask := map[string][]model.Attempt{}
	for _, r := range attempts {
		a, e := store.Decode[model.Attempt](r)
		if e != nil {
			return Report{}, e
		}
		byTask[a.Task] = append(byTask[a.Task], a)
	}
	samples := []Sample{}
	known := map[string]bool{}
	for _, record := range tasks {
		task, e := store.Decode[model.Task](record)
		if e != nil {
			return Report{}, e
		}
		known[task.ID] = true
		usage, e := s.TaskUsage(ctx, task.ID)
		if e != nil {
			return Report{}, e
		}
		samples = append(samples, Sample{Task: task, Usage: usage, Entries: usage.Entries, Attempts: byTask[task.ID], Category: categories[task.ID], Decision: decisions[task.ID], Decisions: decisionHistory[task.ID], Verified: verified(ctx, s, task), Interventions: interventions[task.ID]})
	}
	for id, attempts := range byTask {
		if known[id] {
			continue
		}
		usage, err := s.TaskUsage(ctx, id)
		if err != nil {
			return Report{}, err
		}
		samples = append(samples, Sample{Operation: true, Task: model.Task{ID: id, Stage: "operation"}, Usage: usage, Entries: usage.Entries, Attempts: attempts, Category: categories[id]})
	}
	return Build(samples), nil
}
func verified(ctx context.Context, s *store.Store, t model.Task) bool {
	if !t.ChecksPassed || t.Score < 9 || t.VerificationID == "" || t.ReviewID == "" || t.ReviewedCommit == "" {
		return false
	}
	v, err := s.Get(ctx, "verification", t.VerificationID)
	if err != nil {
		return false
	}
	var evidence struct {
		Stage      string           `json:"stage"`
		Provenance model.Provenance `json:"provenance"`
	}
	if json.Unmarshal(v.Data, &evidence) != nil || evidence.Stage != "complete" {
		return false
	}
	p := evidence.Provenance
	if p.Task != t.ID || p.PlanVersion != t.PlanVersion || p.Head != t.ReviewedCommit || p.Base != t.Base {
		return false
	}
	r, err := s.Get(ctx, "reviews", t.ReviewID)
	if err != nil {
		return false
	}
	review, err := store.Decode[model.Review](r)
	if err != nil || review.Provenance == nil || review.Score < 9 {
		return false
	}
	rp := review.Provenance
	if rp.Task != p.Task || rp.Base != p.Base || rp.Head != p.Head || rp.PolicyHash != p.PolicyHash || rp.PlanVersion != p.PlanVersion || rp.VerificationID != t.VerificationID {
		return false
	}
	for _, f := range review.Findings {
		if f.Blocking && !f.Resolved {
			return false
		}
	}
	return true
}
