package dashboard

import (
	"fmt"
	"strings"

	"github.com/rivo/tview"
	"whirlwind/internal/model"
)

type earlierDirectionTarget struct {
	direction model.Direction
	version   int
	label     string
}

func (d *Dashboard) showEarlierDirections() {
	if d.hasCanonicalIdeas() {
		d.showIdeaHistory(false)
		return
	}
	if d.projectID == "" {
		d.status.SetText("Whirlwind | select a project before opening earlier directions")
		return
	}
	state, err := directionGenerationState(d.snapshot["directions"], d.snapshot["inbox"], d.projectRows, d.projectID)
	if err != nil {
		d.status.SetText("Whirlwind | cannot read direction history: " + err.Error())
		return
	}
	targets := earlierDirectionTargets(state)
	if len(targets) == 0 {
		d.status.SetText("Whirlwind | no earlier directions for this project")
		return
	}

	list := tview.NewList().ShowSecondaryText(false).SetHighlightFullLine(true)
	list.SetBorder(true).SetTitle(" Earlier · Enter chooses open proposals · Esc closes ")
	details := tview.NewTextView().SetScrollable(true)
	details.SetBorder(true).SetTitle(" Read-only rationale ")
	for index := range targets {
		target := targets[index]
		list.AddItem(tview.Escape(target.label), "", 0, func() {
			if earlierDirectionActionable(target.direction) {
				d.confirmDirectionRecord(target.direction, target.version)
				return
			}
			d.showDirectionRationale(target.direction)
		})
	}
	list.SetChangedFunc(func(index int, _, _ string, _ rune) {
		if index < 0 || index >= len(targets) {
			details.SetText("Select an earlier proposal to inspect its saved rationale.")
			return
		}
		details.SetText(directionRationale(targets[index].direction))
	})
	list.SetCurrentItem(0)
	layout := tview.NewFlex().SetDirection(tview.FlexColumn).AddItem(list, 0, 1, true).AddItem(details, 0, 2, false)
	details.SetText(directionRationale(targets[0].direction))
	d.modal = list
	d.SetRoot(layout, true).SetFocus(list)
}

func earlierDirectionTargets(state directionGenerationView) []earlierDirectionTarget {
	var targets []earlierDirectionTarget
	groupIndex := 0
	for _, generation := range state.generations {
		if state.current != nil && generation.groupID == state.current.groupID && generation.legacy == state.current.legacy && generation.day == state.current.day {
			continue
		}
		groupIndex++
		for _, entry := range generation.entries {
			groupLabel := fmt.Sprintf("Earlier generation %d · %s", groupIndex, directionGenerationDay(generation.day))
			if generation.legacy {
				groupLabel = "Legacy · generation unknown"
			}
			category := strings.ToUpper(entry.direction.Category[:min(1, len(entry.direction.Category))])
			if len(entry.direction.Category) > 1 {
				category += entry.direction.Category[1:]
			}
			label := groupLabel + " · " + category
			if entry.direction.Status != "" {
				label += " · " + entry.direction.Status
			}
			label += " — " + entry.direction.Title
			targets = append(targets, earlierDirectionTarget{direction: entry.direction, version: entry.version, label: label})
		}
	}
	return targets
}

func directionGenerationDay(day string) string {
	if day == "" {
		return "date unknown"
	}
	return day
}

func earlierDirectionActionable(direction model.Direction) bool {
	return !direction.Selected && (direction.Status == "" || direction.Status == "proposed")
}

func (d *Dashboard) showDirectionRationale(direction model.Direction) {
	view := tview.NewTextView().SetScrollable(true).SetText(directionRationale(direction))
	if d.hasCurrentDirection(direction.ID) {
		view.SetBorder(true).SetTitle(" Direction rationale · Esc returns to Inbox · a choose direction / prepare plan ")
		d.directionChoiceFromRationale = true
	} else {
		view.SetBorder(true).SetTitle(" Earlier direction rationale · Esc returns to history ")
		d.directionChoiceFromRationale = false
	}
	d.modal = view
	d.SetRoot(view, true).SetFocus(view)
}

func (d *Dashboard) hasCurrentDirection(id string) bool {
	for _, direction := range d.directionRows {
		if direction.ID == id {
			return true
		}
	}
	return false
}
