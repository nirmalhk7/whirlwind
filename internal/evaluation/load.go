package evaluation

import (
	"context"
	"encoding/json"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

// Load reads durable evidence only. It never starts inference or runs checks.
func Load(ctx context.Context, s *store.Store) (Report, error) {
	records, err := loadEvaluationRecords(ctx, s)
	if err != nil {
		return Report{}, err
	}
	data, err := loadSampleData(records)
	if err != nil {
		return Report{}, err
	}
	samples, known, err := taskSamples(ctx, s, records["tasks"], data)
	if err != nil {
		return Report{}, err
	}
	operationSamples, err := unknownTaskSamples(ctx, s, data.attempts, known, data.categories)
	if err != nil {
		return Report{}, err
	}
	samples = append(samples, operationSamples...)
	return Build(samples), nil
}

func loadSampleData(records map[string][]model.Record) (sampleData, error) {
	byTask, err := attemptsByTask(records["sessions"])
	if err != nil {
		return sampleData{}, err
	}
	decisions, history, err := loadDecisions(records["outcomes"])
	if err != nil {
		return sampleData{}, err
	}
	categories, err := loadCategories(records["metrics"])
	if err != nil {
		return sampleData{}, err
	}
	interventions, err := countInterventions(records["audit"])
	if err != nil {
		return sampleData{}, err
	}
	return sampleData{attempts: byTask, categories: categories, decisions: decisions, history: history, interventions: interventions}, nil
}

func unknownTaskSamples(ctx context.Context, s *store.Store, byTask map[string][]model.Attempt, known map[string]bool, categories map[string]string) ([]Sample, error) {
	samples := []Sample{}
	for id, attempts := range byTask {
		if known[id] {
			continue
		}
		usage, err := s.TaskUsage(ctx, id)
		if err != nil {
			return nil, err
		}
		samples = append(samples, Sample{Operation: true, Task: model.Task{ID: id, Stage: "operation"}, Usage: usage, Entries: usage.Entries, Attempts: attempts, Category: categories[id]})
	}
	return samples, nil
}

func loadEvaluationRecords(ctx context.Context, s *store.Store) (map[string][]model.Record, error) {
	records := make(map[string][]model.Record, 5)
	for _, kind := range []string{"tasks", "sessions", "outcomes", "metrics", "audit"} {
		items, err := s.List(ctx, kind)
		if err != nil {
			return nil, err
		}
		records[kind] = items
	}
	return records, nil
}

func loadDecisions(records []model.Record) (map[string]string, map[string][]string, error) {
	decisions, history := map[string]string{}, map[string][]string{}
	for _, record := range records {
		var outcome struct {
			Task     string `json:"task"`
			Decision string `json:"human_decision"`
		}
		if err := json.Unmarshal(record.Data, &outcome); err != nil {
			return nil, nil, err
		}
		decisions[outcome.Task] = outcome.Decision
		history[outcome.Task] = append(history[outcome.Task], outcome.Decision)
	}
	return decisions, history, nil
}

func loadCategories(records []model.Record) (map[string]string, error) {
	categories := map[string]string{}
	for _, record := range records {
		var metric struct {
			Task     string `json:"task"`
			Category string `json:"category"`
		}
		if err := json.Unmarshal(record.Data, &metric); err != nil {
			return nil, err
		}
		categories[metric.Task] = metric.Category
	}
	return categories, nil
}

func countInterventions(records []model.Record) (map[string]int, error) {
	interventions := map[string]int{}
	for _, record := range records {
		audit, err := store.Decode[model.Audit](record)
		if err != nil {
			return nil, err
		}
		if audit.Source == "local_operator" {
			interventions[audit.Task]++
		}
	}
	return interventions, nil
}

func attemptsByTask(records []model.Record) (map[string][]model.Attempt, error) {
	byTask := map[string][]model.Attempt{}
	for _, record := range records {
		attempt, err := store.Decode[model.Attempt](record)
		if err != nil {
			return nil, err
		}
		byTask[attempt.Task] = append(byTask[attempt.Task], attempt)
	}
	return byTask, nil
}

type sampleData struct {
	attempts      map[string][]model.Attempt
	categories    map[string]string
	decisions     map[string]string
	history       map[string][]string
	interventions map[string]int
}

func taskSamples(ctx context.Context, s *store.Store, records []model.Record, data sampleData) ([]Sample, map[string]bool, error) {
	samples, known := []Sample{}, map[string]bool{}
	for _, record := range records {
		task, err := store.Decode[model.Task](record)
		if err != nil {
			return nil, nil, err
		}
		known[task.ID] = true
		usage, err := s.TaskUsage(ctx, task.ID)
		if err != nil {
			return nil, nil, err
		}
		samples = append(samples, Sample{Task: task, Usage: usage, Entries: usage.Entries, Attempts: data.attempts[task.ID], Category: data.categories[task.ID], Decision: data.decisions[task.ID], Decisions: data.history[task.ID], Verified: verified(ctx, s, task), Interventions: data.interventions[task.ID]})
	}
	return samples, known, nil
}
