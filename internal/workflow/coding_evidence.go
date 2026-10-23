package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"whirlwind/internal/model"
)

func (e *Engine) requireTDDEvidence(ctx context.Context, t model.Task) error {
	files, err := tddChangedFiles(ctx, t.Worktree, t.Base, "")
	if err != nil {
		return err
	}
	if !hasExecutableChange(files) {
		return nil
	}
	pairs, current, err := e.loadTDDEvidence(ctx, t)
	if err != nil {
		return err
	}
	if err := requireTDDFileCoverage(files, pairs, current); err != nil {
		return err
	}
	return e.requireTDDUnitCoverage(ctx, t, pairs)
}

func hasExecutableChange(files []string) bool {
	for _, file := range files {
		if file != "" && !testPath(file) && productionPath(file) {
			return true
		}
	}
	return false
}

func (e *Engine) loadTDDEvidence(ctx context.Context, t model.Task) ([]tddPair, map[string]string, error) {
	rows, err := e.Store.List(ctx, "tdd_checks")
	if err != nil {
		return nil, nil, err
	}
	_, _, _, current, err := codingSnapshotManifest(ctx, t.Worktree)
	if err != nil {
		return nil, nil, err
	}
	return currentTDDPairs(rows, t, current), current, nil
}

// Production coverage comes from changes between paired red and green phases,
// never an unrelated file merely present in the passing snapshot.
func requireTDDFileCoverage(files []string, pairs []tddPair, current map[string]string) error {
	for _, file := range files {
		if !productionPath(file) && !testPath(file) {
			continue
		}
		if !coveredByTDDPair(pairs, file, current) {
			return fmt.Errorf("TDD evidence is missing or stale for %s; rerun its test-first regression through run_check", file)
		}
	}
	return nil
}

func coveredByTDDPair(pairs []tddPair, file string, current map[string]string) bool {
	for _, pair := range pairs {
		if pair.covers(file, current) {
			return true
		}
	}
	return false
}

func (e *Engine) requireTDDUnitCoverage(ctx context.Context, t model.Task, pairs []tddPair) error {
	if t.Plan == nil {
		return errors.New("TDD evidence requires an implementation plan")
	}
	for _, unit := range t.Plan.Units {
		if err := requireTDDCheckpoint(ctx, t.Worktree, unit, pairs); err != nil {
			return err
		}
	}
	return nil
}

func requireTDDCheckpoint(ctx context.Context, worktree string, unit model.Unit, pairs []tddPair) error {
	if unit.Base == "" || unit.Commit == "" {
		return fmt.Errorf("TDD evidence requires completed checkpoint for unit %s", unit.ID)
	}
	changed, err := tddChangedFiles(ctx, worktree, unit.Base, unit.Commit)
	if err != nil {
		return err
	}
	for _, file := range changed {
		if testPath(file) || !productionPath(file) {
			continue
		}
		if !coveredByTDDUnit(pairs, unit.ID, file) {
			return fmt.Errorf("TDD evidence required for executable unit %s path %s; another unit's evidence cannot authorize it", unit.ID, file)
		}
	}
	return nil
}

func coveredByTDDUnit(pairs []tddPair, unit, file string) bool {
	for _, pair := range pairs {
		if pair.green.Unit == unit && pair.changedProduction(file) {
			return true
		}
	}
	return false
}

type tddPair struct{ red, green tddCheck }

func currentTDDPairs(rows []model.Record, task model.Task, current map[string]string) []tddPair {
	type key struct{ unit, check string }
	reds := map[key]tddCheck{}
	var pairs []tddPair
	for _, row := range rows {
		check, valid := usableTDDCheck(row, task)
		if !valid {
			continue
		}
		id := key{check.Unit, check.CheckID}
		if check.Phase == "red" && !check.Passed {
			reds[id] = check
		}
		red, exists := reds[id]
		if exists && validTDDGreen(red, check, current) {
			pairs = append(pairs, tddPair{red: red, green: check})
		}
	}
	return pairs
}

func usableTDDCheck(row model.Record, task model.Task) (tddCheck, bool) {
	var check tddCheck
	if json.Unmarshal(row.Data, &check) != nil {
		return check, false
	}
	valid := check.Task == task.ID && check.PlanVersion == task.PlanVersion && len(check.Files) > 0
	return check, valid && check.Snapshot != "" && check.Tests != "" && check.Production != ""
}

func validTDDGreen(red, green tddCheck, current map[string]string) bool {
	if green.Phase != "green" || !green.Passed || red.Tests != green.Tests || red.Production == green.Production {
		return false
	}
	return matchingTDDTests(green.Files, current)
}

func (p tddPair) changedProduction(file string) bool {
	return !testPath(file) && productionPath(file) && tddFileValue(p.red.Files, file) != tddFileValue(p.green.Files, file)
}
func (p tddPair) covers(file string, current map[string]string) bool {
	if tddFileValue(p.green.Files, file) != tddFileValue(current, file) {
		return false
	}
	if testPath(file) {
		_, known := p.green.Files[file]
		return known
	}
	return p.changedProduction(file)
}
