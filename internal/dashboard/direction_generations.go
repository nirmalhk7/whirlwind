package dashboard

import (
	"sort"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

type directionEntry struct {
	direction    model.Direction
	version      int
	createdOrder int64
	updatedAt    string
}

type directionGeneration struct {
	groupID       string
	day           string
	entries       []directionEntry
	categoryCount int
	missing       []string
	complete      bool
	legacy        bool
	createdOrder  int64
	updatedAt     string
}

type directionGenerationView struct {
	generations []directionGeneration
	current     *directionGeneration
	paused      *model.Notification
	marker      string
}

func directionGenerations(records []model.Record, projectID string) ([]directionGeneration, error) {
	groups, err := collectDirectionGenerations(records, projectID)
	if err != nil {
		return nil, err
	}
	result := make([]directionGeneration, 0, len(groups))
	for _, group := range groups {
		summarizeDirectionGeneration(group)
		result = append(result, *group)
	}
	sort.SliceStable(result, func(i, j int) bool { return directionGenerationBefore(result[i], result[j]) })
	return result, nil
}

func collectDirectionGenerations(records []model.Record, projectID string) (map[string]*directionGeneration, error) {
	groups := make(map[string]*directionGeneration)
	for _, record := range records {
		direction, err := store.Decode[model.Direction](record)
		if err != nil {
			return nil, err
		}
		if direction.ProjectID != projectID {
			continue
		}
		addDirectionRecord(groups, record, direction)
	}
	return groups, nil
}

func addDirectionRecord(groups map[string]*directionGeneration, record model.Record, direction model.Direction) {
	key := direction.GroupID
	if key == "" {
		key = "\x00legacy:" + direction.Day
	}
	group := groups[key]
	if group == nil {
		group = &directionGeneration{groupID: direction.GroupID, legacy: direction.GroupID == ""}
		groups[key] = group
	}
	group.entries = append(group.entries, directionEntry{direction: direction, version: record.Version, createdOrder: record.CreatedOrder, updatedAt: record.UpdatedAt})
	if direction.Day != "" {
		group.day = direction.Day
	}
	if record.CreatedOrder > group.createdOrder {
		group.createdOrder = record.CreatedOrder
	}
	if record.UpdatedAt > group.updatedAt {
		group.updatedAt = record.UpdatedAt
	}
}

func summarizeDirectionGeneration(group *directionGeneration) {
	present := make(map[string]bool, len(model.DirectionCategories()))
	for _, entry := range group.entries {
		if model.IsDirectionCategory(entry.direction.Category) {
			present[entry.direction.Category] = true
		}
	}
	for _, category := range model.DirectionCategories() {
		if present[category] {
			group.categoryCount++
			continue
		}
		group.missing = append(group.missing, category)
	}
	group.complete = !group.legacy && len(group.entries) == len(model.DirectionCategories()) && group.categoryCount == len(model.DirectionCategories())
	sort.SliceStable(group.entries, func(i, j int) bool { return directionEntryBefore(group.entries[i], group.entries[j]) })
}

func directionEntryBefore(left, right directionEntry) bool {
	leftOrder := categoryOrder(left.direction.Category)
	rightOrder := categoryOrder(right.direction.Category)
	if leftOrder != rightOrder {
		return leftOrder < rightOrder
	}
	return left.createdOrder < right.createdOrder
}

func directionGenerationBefore(left, right directionGeneration) bool {
	if left.createdOrder != right.createdOrder {
		return left.createdOrder > right.createdOrder
	}
	if left.updatedAt != right.updatedAt {
		return left.updatedAt > right.updatedAt
	}
	if left.legacy != right.legacy {
		return !left.legacy
	}
	return left.day > right.day
}

func categoryOrder(category string) int {
	for index, expected := range model.DirectionCategories() {
		if category == expected {
			return index
		}
	}
	return len(model.DirectionCategories())
}
