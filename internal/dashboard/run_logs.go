package dashboard

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/rivo/tview"

	"whirlwind/internal/store"
)

func (d *Dashboard) showRunLogs(index int) {
	if index < 0 || index >= len(d.runRows) {
		return
	}
	d.selectRun(index, "", "", 0)
	row := d.runRows[index]
	if row.operation != nil {
		view := tview.NewTextView().SetText(operationRunDetails(row.id, *row.operation))
		title := " Ideation run · Esc to close "
		if row.operation.Kind == "project_setup" {
			title = " Project setup · Esc to close "
		}
		view.SetBorder(true).SetTitle(title)
		d.modal = view
		d.SetRoot(view, true).SetFocus(view)
		return
	}
	var err error
	if !d.Suspend(func() { err = followRunLogs(filepath.Join(d.dataDirectory, "whirlwind.db"), row.id) }) {
		err = fmt.Errorf("terminal cannot be suspended")
	}
	if err != nil {
		d.status.SetText("Whirlwind | run logs failed: " + err.Error())
	} else {
		d.status.SetText("Whirlwind | returned from run logs")
	}
}

func followRunLogs(path, id string) error {
	database, err := store.OpenReadOnly(path)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()
	logPath, err := database.RunLogPath(context.Background(), id)
	if err != nil {
		return err
	}
	return runTerminalTail(context.Background(), logPath)
}
