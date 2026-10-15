package workflow

import (
	"errors"
	"regexp"
	"whirlwind/internal/model"
)

var unitIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

type unitGraph struct {
	units map[string]model.Unit
	state map[string]int
}

// ValidateUnits validates names before walking all dependency paths for missing units and cycles.
func ValidateUnits(units []model.Unit) error {
	graph, err := newUnitGraph(units)
	if err != nil {
		return err
	}
	for id := range graph.units {
		if err = graph.visit(id); err != nil {
			return err
		}
	}
	return nil
}
func newUnitGraph(units []model.Unit) (unitGraph, error) {
	graph := unitGraph{units: map[string]model.Unit{}, state: map[string]int{}}
	for _, unit := range units {
		if !unitIDPattern.MatchString(unit.ID) || unit.ID == "integration" {
			return graph, errors.New("unit id required")
		}
		if _, found := graph.units[unit.ID]; found {
			return graph, errors.New("duplicate unit")
		}
		graph.units[unit.ID] = unit
	}
	return graph, nil
}
func (g unitGraph) visit(id string) error {
	unit, ok := g.units[id]
	if !ok {
		return errors.New("unknown dependency")
	}
	if g.state[id] == 1 {
		return errors.New("dependency cycle")
	}
	if g.state[id] == 2 {
		return nil
	}
	g.state[id] = 1
	for _, dependency := range unit.Dependencies {
		if err := g.visit(dependency); err != nil {
			return err
		}
	}
	g.state[id] = 2
	return nil
}
