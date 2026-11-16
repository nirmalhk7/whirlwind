package dashboard

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func directionGenerationState(records, inbox []model.Record, projects []model.Project, projectID string) (directionGenerationView, error) {
	generations, err := directionGenerations(records, projectID)
	if err != nil {
		return directionGenerationView{}, err
	}
	state := directionGenerationView{generations: generations}
	if len(generations) > 0 {
		state.current = &state.generations[0]
	}
	paused, pauseRecord, ok := latestPausedIdeation(inbox, projects, projectID)
	if pauseIsNewer(ok, pauseRecord, state.current) {
		state.paused = &paused
		setPausedGenerationMarker(&state, paused)
		return state, nil
	}
	state.marker = currentGenerationMarker(state.current, len(generations))
	return state, nil
}

func pauseIsNewer(found bool, pauseRecord model.Record, current *directionGeneration) bool {
	return found && (current == nil || pauseRecord.CreatedOrder > current.createdOrder || timestampAfter(pauseRecord, current.updatedAt))
}

func setPausedGenerationMarker(state *directionGenerationView, paused model.Notification) {
	switch {
	case isExplicitEmptyIdeationNotice(paused.Message):
		state.current = nil
		state.marker = "Latest generation incomplete · 0/4 categories recorded · no new directions"
	case state.current == nil:
		state.marker = "Latest ideation paused · proposal count unknown"
	case state.current.legacy:
		state.marker = "Legacy directions · generation unknown · latest attempt paused"
	case !state.current.complete:
		state.marker = incompleteGenerationMarker(*state.current) + " · latest attempt paused"
	default:
		state.marker = "Latest attempt paused · showing last saved complete generation"
	}
}

func currentGenerationMarker(current *directionGeneration, count int) string {
	switch {
	case current == nil && count > 0:
		return "Earlier directions available · press o to inspect"
	case current == nil:
		return ""
	case current.legacy:
		return "Legacy directions · generation unknown · press o to inspect"
	case current.complete:
		return ""
	default:
		return incompleteGenerationMarker(*current)
	}
}

func isExplicitEmptyIdeationNotice(message string) bool {
	message = strings.ToLower(message)
	if !strings.Contains(message, "ideation incomplete") {
		return false
	}
	var categories string
	if _, rest, ok := strings.Cut(message, "missing categories:"); ok {
		categories = rest
	} else if _, rest, ok := strings.Cut(message, "no worthwhile new direction for categories:"); ok {
		categories = rest
	} else {
		return false
	}
	present := make(map[string]bool, len(ideationDirectionCategories))
	for _, category := range strings.FieldsFunc(categories, func(char rune) bool { return !unicode.IsLetter(char) }) {
		present[category] = true
	}
	for _, category := range ideationDirectionCategories {
		if !present[category] {
			return false
		}
	}
	return true
}

func latestPausedIdeation(records []model.Record, projects []model.Project, projectID string) (model.Notification, model.Record, bool) {
	projectName, ok := uniqueProjectName(projects, projectID)
	if !ok {
		return model.Notification{}, model.Record{}, false
	}
	return latestPausedForProject(records, projectName)
}

func uniqueProjectName(projects []model.Project, projectID string) (string, bool) {
	projectName := ""
	for _, project := range projects {
		if project.ID == projectID {
			projectName = project.Name
		}
	}
	if projectName == "" {
		return "", false
	}
	nameMatches := 0
	for _, project := range projects {
		if project.Name == projectName {
			nameMatches++
		}
	}
	return projectName, nameMatches == 1
}

func latestPausedForProject(records []model.Record, projectName string) (model.Notification, model.Record, bool) {
	var latest model.Notification
	var latestRecord model.Record
	found := false
	for _, record := range records {
		notice, err := store.Decode[model.Notification](record)
		if err != nil || notice.Resolved || notice.Kind != "ideation_paused" || !strings.HasPrefix(notice.Message, projectName+":") {
			continue
		}
		stamp := record
		if stamp.UpdatedAt == "" {
			stamp.UpdatedAt = notice.LastAt
		}
		if !found || timestampAfter(stamp, latestRecord.UpdatedAt) {
			latest, latestRecord, found = notice, stamp, true
		}
	}
	return latest, latestRecord, found
}

func timestampAfter(record model.Record, timestamp string) bool {
	current := record.UpdatedAt
	if current == "" {
		return false
	}
	if timestamp == "" {
		return true
	}
	currentTime, currentErr := time.Parse(time.RFC3339Nano, current)
	previousTime, previousErr := time.Parse(time.RFC3339Nano, timestamp)
	if currentErr == nil && previousErr == nil {
		return currentTime.After(previousTime)
	}
	return current > timestamp
}

func incompleteGenerationMarker(generation directionGeneration) string {
	marker := fmt.Sprintf("Latest generation incomplete · %d/4 categories", generation.categoryCount)
	if len(generation.missing) > 0 {
		missing := make([]string, len(generation.missing))
		for index, category := range generation.missing {
			missing[index] = strings.ToUpper(category[:1]) + category[1:]
		}
		marker += " · missing " + strings.Join(missing, ", ")
	} else if !generation.complete {
		marker += " · invalid category count"
	}
	return marker
}
