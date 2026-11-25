package dashboard

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/rivo/tview"
)

// Preserve full multiline strings and literal terminal markup in the editor.
func messageText(message any) string {
	encoded, _ := json.Marshal(message)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(encoded, &fields)
	keys := slices.Sorted(maps.Keys(fields))
	var text strings.Builder
	for _, key := range keys {
		var value string
		if json.Unmarshal(fields[key], &value) != nil {
			value = string(fields[key])
		}
		fmt.Fprintf(&text, "%s:\n%s\n\n", key, value)
	}
	return text.String()
}

func (d *Dashboard) viewMessage(index int) {
	if d.openProjectSetupNotice(index) {
		return
	}
	if index >= 0 && index < len(d.directionRows) {
		d.directions.SetCurrentItem(index)
		if idea, version, ok := d.ideaRecord(d.directionRows[index].ID); ok {
			d.showIdeaRecord(idea, version)
			return
		}
		d.showDirectionRationale(d.directionRows[index])
		d.directionChoiceFromRationale = true
		return
	}
	if index >= 0 && index < len(d.inboxTaskIDs) {
		for i, task := range d.planRows {
			if task.ID == d.inboxTaskIDs[index] && task.Stage == "plan_approval" && task.PlanDocument != nil {
				d.plans.SetCurrentItem(i)
				d.selectPlan(i, "", "", 0)
				d.reviewPlanForm()
				return
			}
		}
	}
	d.viewSavedMessage(index)
}

func (d *Dashboard) viewSavedMessage(index int) {
	if index < 0 || index >= len(d.inboxMessages) {
		return
	}
	file, err := os.CreateTemp("", "whirlwind-message-*.txt")
	if err != nil {
		d.status.SetText("Whirlwind | cannot open message: " + err.Error())
		return
	}
	defer d.removeEditorFile(file.Name())
	_, err = file.WriteString(d.inboxMessages[index])
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		if !d.Suspend(func() {
			command := exec.Command("vim", "-R", "--", file.Name())
			command.Stdin = os.Stdin
			command.Stdout = os.Stdout
			command.Stderr = os.Stderr
			err = command.Run()
		}) {
			err = fmt.Errorf("terminal cannot be suspended")
		}
	}
	if err != nil {
		d.status.SetText("Whirlwind | Vim failed: " + err.Error())
	}
}

func (d *Dashboard) showKeyboardHelp() {
	view := tview.NewTextView().SetScrollable(true).SetText(strings.ReplaceAll(keyboardShortcuts, " | ", "\n") + "\n\nVim: :wq submits; unchanged cancels.\nRun logs: Esc or Ctrl-C returns to dashboard.")
	view.SetBorder(true).SetTitle(" Keyboard instructions · Esc to close ")
	d.modal = view
	d.SetRoot(view, true).SetFocus(view)
}
