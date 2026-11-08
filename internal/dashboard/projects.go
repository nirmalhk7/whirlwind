package dashboard

import (
	"path/filepath"

	"github.com/rivo/tview"
	"whirlwind/internal/control"
	"whirlwind/internal/model"
)

func (d *Dashboard) configuredProjects(projects []model.Project, snapshot control.Snapshot) ([]model.Project, error) {
	groups, err := decodeRecords[[]model.ProjectStatus](snapshot["project_status"])
	if err != nil {
		return nil, err
	}
	d.projectHealth = map[string]model.ProjectStatus{}
	for _, statuses := range groups {
		for _, status := range statuses {
			id := status.ProjectID
			if id == "" {
				id = "configured:" + status.Path
				projects = append(projects, model.Project{ID: id, Name: filepath.Base(status.Path), Path: status.Path})
			}
			d.projectHealth[id] = status
		}
	}
	return projects, nil
}

func (d *Dashboard) projectLabel(project model.Project) string {
	label := tview.Escape(project.Name)
	status, ok := d.projectHealth[project.ID]
	if !ok {
		return label
	}
	if status.Status == "OK" {
		return label + " [green]OK[-]"
	}
	return label + " [red]ERR[-]"
}

func (d *Dashboard) showProjectHealth() {
	if len(d.planRows) != 0 {
		return
	}
	if status, ok := d.projectHealth[d.projectID]; ok {
		text := status.Path + "\nGit: " + status.Status
		if status.Detail != "" {
			text += "\n" + status.Detail
		}
		d.detail.SetText(text + "\n\nNo saved plans for this project.")
	}
}
