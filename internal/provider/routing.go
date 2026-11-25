package provider

import (
	"errors"
	"slices"
	"sort"

	"whirlwind/internal/model"
)

// Route filters providers by availability, quota, and role, then ranks candidates by outcomes, configured preferences, and cost.
func Route(ps []model.Provider, role string, preferred []string, outcomes map[string]float64) (model.Provider, error) {
	c := []model.Provider{}
	for _, p := range ps {
		if !providerReady(p) {
			continue
		}
		if slices.Contains(p.Capabilities, role) {
			c = append(c, p)
		}
	}
	if len(c) == 0 {
		return model.Provider{}, errors.New("no available provider with required capability")
	}
	rank := func(p model.Provider) float64 {
		v := outcomes[p.ID] * 10
		for i, id := range preferred {
			if id == p.ID {
				v += float64(len(preferred) - i)
			}
		}
		if p.Cost != nil {
			v -= *p.Cost
		}
		return v
	}
	sort.SliceStable(c, func(i, j int) bool { return rank(c[i]) > rank(c[j]) })
	return c[0], nil
}
