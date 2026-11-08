package dashboard

import (
	"github.com/rivo/tview"
	"whirlwind/internal/control"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

// Present updates widgets on the terminal event loop, keeping durable identities
// selected even when polling changes the order of repository records.
func (d *Dashboard) Present(snapshot control.Snapshot) {
	projects, err := decodeRecords[model.Project](snapshot["projects"])
	if err != nil {
		d.detail.SetText("Cannot read project state: " + err.Error())
		return
	}
	projects, err = d.configuredProjects(projects, snapshot)
	if err != nil {
		d.detail.SetText("Cannot read project health: " + err.Error())
		return
	}
	d.snapshot, d.projectRows, d.updating = snapshot, projects, true
	d.refreshAgents()
	d.projects.Clear()
	selected := 0
	for i, project := range projects {
		d.projects.AddItem(d.projectLabel(project), "", 0, nil)
		if project.ID == d.projectID {
			selected = i
		}
	}
	if len(projects) == 0 {
		d.projects.AddItem("No projects", "", 0, nil)
		d.projectID = ""
	} else {
		d.projectID = projects[selected].ID
		d.projects.SetCurrentItem(selected)
	}
	d.updating = false
	d.refreshDirections()
	d.refreshPlans()
	d.showProjectHealth()
}

func decodeRecords[T any](records []model.Record) ([]T, error) {
	values := make([]T, 0, len(records))
	for _, record := range records {
		value, err := store.Decode[T](record)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func (d *Dashboard) selectProject(index int, _, _ string, _ rune) {
	if d.updating || index < 0 || index >= len(d.projectRows) {
		return
	}
	d.projectID, d.directionID, d.planID = d.projectRows[index].ID, "", ""
	d.refreshDirections()
	d.refreshPlans()
	d.showProjectHealth()
}

func (d *Dashboard) refreshDirections() {
	directions, err := decodeRecords[model.Direction](d.snapshot["directions"])
	if err != nil {
		d.detail.SetText("Cannot read direction state: " + err.Error())
		return
	}
	d.updating = true
	d.directions.Clear()
	d.directionRows = nil
	selected := 0
	for _, direction := range directions {
		if direction.ProjectID != d.projectID || (direction.Status != "" && direction.Status != "proposed" && direction.Status != "selected") {
			continue
		}
		if direction.ID == d.directionID {
			selected = len(d.directionRows)
		}
		d.directionRows = append(d.directionRows, direction)
		status := direction.Status
		d.directions.AddItem(tview.Escape(direction.Title+" · "+status), "", 0, nil)
	}
	if len(d.directionRows) == 0 {
		d.directions.AddItem("No directions", "", 0, nil)
		d.directionID = ""
	} else {
		d.directionID = d.directionRows[selected].ID
		d.directions.SetCurrentItem(selected)
	}
	d.updating = false
}

func (d *Dashboard) selectDirection(index int, _, _ string, _ rune) {
	if d.updating || index < 0 || index >= len(d.directionRows) {
		return
	}
	d.directionID = d.directionRows[index].ID
}

func (d *Dashboard) refreshPlans() {
	plans, err := decodeRecords[model.Task](d.snapshot["tasks"])
	if err != nil {
		d.detail.SetText("Cannot read plan state: " + err.Error())
		return
	}
	d.updating = true
	d.plans.Clear()
	d.planRows = nil
	selected := 0
	for _, plan := range plans {
		if plan.ProjectID != d.projectID {
			continue
		}
		if plan.ID == d.planID {
			selected = len(d.planRows)
		}
		d.planRows = append(d.planRows, plan)
		d.plans.AddItem(tview.Escape(planTitle(plan)), "", 0, nil)
	}
	if len(d.planRows) == 0 {
		d.plans.AddItem("No plans", "", 0, nil)
		d.detail.SetText("No saved plans for this project.")
	} else {
		d.plans.SetCurrentItem(selected)
	}
	d.updating = false
	d.selectPlan(selected, "", "", 0)
}

func planTitle(plan model.Task) string {
	if plan.Plan != nil && plan.Plan.Objective != "" {
		return plan.Plan.Objective
	}
	if plan.Issue.Title != "" {
		return plan.Issue.Title
	}
	return plan.ID
}
