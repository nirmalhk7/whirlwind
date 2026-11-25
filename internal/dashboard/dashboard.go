// Package dashboard presents local supervisor state in the terminal.
package dashboard

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"whirlwind/internal/control"
	"whirlwind/internal/model"
	"whirlwind/internal/store"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const inboxTitle = " Inbox "

type Dashboard struct {
	*tview.Application
	projects                     *tview.List
	directions                   *tview.List
	inbox                        *tview.List
	inboxIdeaCount               int
	inboxTaskIDs                 []string
	setupNotice                  *projectSetupNotice
	plans                        *tview.List
	runs                         *tview.List
	runRows                      []runRow
	runID                        string
	taskView                     bool
	detail                       *tview.TextView
	status                       *tview.TextView
	runnerStatus                 *tview.TextView
	agents                       *tview.TextView
	agentInspection              *tview.TextView
	focus                        int
	snapshot                     control.Snapshot
	root                         tview.Primitive
	modal                        tview.Primitive
	ideaDetailView               *tview.TextView
	planReviewView               *tview.TextView
	planReviewForm               *tview.Form
	planReviewVim                func()
	directionChoiceFromRationale bool
	commands                     func(context.Context, control.Request) (json.RawMessage, error)
	projectRows                  []model.Project
	projectHealth                map[string]model.ProjectStatus
	directionRows                []model.Direction
	planRows                     []model.ExecutionPlan
	projectID                    string
	projectAliases               map[string]string
	directionID                  string
	planID                       string
	updating                     bool
	dataDirectory                string
	inboxMessages                []string
}

func New(dataDirectories ...string) *Dashboard {
	d := &Dashboard{Application: tview.NewApplication(), projects: tview.NewList().ShowSecondaryText(false), directions: tview.NewList().ShowSecondaryText(false), plans: tview.NewList().ShowSecondaryText(false), detail: tview.NewTextView().SetScrollable(true)}
	d.dataDirectory = os.Getenv("WHIRLWIND_DATA_DIR")
	if len(dataDirectories) > 0 {
		d.dataDirectory = dataDirectories[0]
	}
	if d.dataDirectory == "" {
		home, _ := os.UserHomeDir()
		d.dataDirectory = filepath.Join(home, ".local", "share", "whirlwind", "parity-v1")
	}
	d.EnableMouse(true)
	d.initializeInbox()
	d.projects.SetSelectedBackgroundColor(tcell.ColorDefault).SetSelectedTextColor(tcell.ColorWhite)
	d.runs = tview.NewList().ShowSecondaryText(true)
	d.runs.SetBorder(true).SetTitle(" Runs ")
	d.runs.AddItem("No runs", "", 0, nil)
	d.runs.SetChangedFunc(d.selectRun)
	d.runs.SetHighlightFullLine(true).SetSelectedBackgroundColor(tcell.ColorWhite).SetSelectedTextColor(tcell.ColorBlack)
	d.runs.SetSelectedFunc(func(index int, _, _ string, _ rune) { d.showRunLogs(index) })
	d.projects.SetBorder(true).SetTitle(" Projects ")
	d.directions.SetBorder(true).SetTitle(inboxTitle)
	d.plans.SetBorder(true).SetTitle(" Plans ")
	d.agents = tview.NewTextView().SetDynamicColors(true).SetScrollable(true)
	d.agents.SetBorder(true).SetTitle(" Agents · Enter ")
	d.agents.SetText("No configured agents")
	d.projects.AddItem("No projects", "", 0, nil)
	d.directions.AddItem("No inbox items", "", 0, nil)
	d.plans.AddItem("No plans", "", 0, nil)
	d.detail.SetText("Select a project, then a run to inspect agent history.")
	d.status = tview.NewTextView().SetText("Whirlwind | waiting for local supervisor state")
	d.runnerStatus = tview.NewTextView().SetTextAlign(tview.AlignCenter)
	d.updateRunnerStatus(nil)
	navigation := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(d.projects, 0, 1, true).AddItem(d.agents, 0, 1, false).AddItem(&runListView{List: d.runs}, 0, 1, false)
	d.detail.SetBorder(true).SetTitle(inboxTitle)
	panels := tview.NewFlex().AddItem(navigation, 0, 3, true).AddItem(d.inbox, 0, 10, false)
	help := tview.NewTextView().SetText(keyboardShortcuts).SetWrap(false)
	statusRow := tview.NewFlex().AddItem(d.status, 0, 1, false).AddItem(d.runnerStatus, 8, 0, false)
	d.root = &dashboardLayout{Flex: tview.NewFlex().SetDirection(tview.FlexRow).AddItem(panels, 0, 1, true).AddItem(statusRow, 1, 0, false).AddItem(help, 1, 0, false), help: help}

	for _, panel := range []struct {
		box   *tview.Box
		index int
	}{
		{d.projects.Box, 0}, {d.agents.Box, 4}, {d.runs.Box, 2}, {d.inbox.Box, 3},
	} {
		panel.box.SetBorderColor(tcell.ColorWhite)
		panel.box.SetFocusFunc(func() {
			d.focus = panel.index
			panel.box.SetBorderColor(tcell.ColorAqua).SetTitleColor(tcell.ColorAqua)
		})
		panel.box.SetBlurFunc(func() { panel.box.SetBorderColor(tcell.ColorWhite).SetTitleColor(tcell.ColorWhite) })
	}

	d.SetRoot(d.root, true).SetFocus(d.projects).SetInputCapture(d.input)
	d.projects.SetChangedFunc(d.selectProject)
	d.directions.SetChangedFunc(d.selectDirection)
	d.directions.SetSelectedFunc(func(_ int, _, _ string, _ rune) { d.confirmDirection() })
	d.plans.SetChangedFunc(d.selectPlan)
	return d
}

func (d *Dashboard) initializeInbox() {
	d.inbox = tview.NewList().ShowSecondaryText(false).SetHighlightFullLine(true).SetSelectedBackgroundColor(tcell.ColorWhite).SetSelectedTextColor(tcell.ColorBlack)
	d.inbox.SetMouseCapture(d.captureInboxClick)
	d.inbox.SetBorder(true).SetTitle(inboxTitle)
	d.inbox.SetSelectedFunc(func(index int, _, _ string, _ rune) {
		if index >= 0 && index < len(d.directionRows) {
			d.directions.SetCurrentItem(index)
			d.confirmDirection()
		} else if index >= 0 && index < len(d.inboxTaskIDs) {
			d.openInboxSelection(index)
		}
	})
	d.inbox.SetChangedFunc(func(index int, _, _ string, _ rune) {
		if !d.updating && index < len(d.directionRows) {
			d.directions.SetCurrentItem(index)
		}
	})
}

func (d *Dashboard) captureInboxClick(action tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
	if action != tview.MouseLeftClick {
		return action, event
	}
	x, y := event.Position()
	left, top, width, height := d.inbox.GetInnerRect()
	offset, _ := d.inbox.GetOffset()
	if x < left || x >= left+width || y < top || y >= top+height {
		return action, event
	}
	index := offset + y - top
	if index >= len(d.inboxMessages) {
		return action, event
	}
	d.inbox.SetCurrentItem(index)
	d.focus = 3
	d.SetFocus(d.inbox)
	d.viewMessage(index)
	return action, nil
}

func (d *Dashboard) SetCommandHandler(handler func(context.Context, control.Request) (json.RawMessage, error)) {
	d.commands = handler
}

func (d *Dashboard) submit(request control.Request) {
	if d.commands == nil {
		d.status.SetText("Whirlwind | local supervisor unavailable; decisions remain saved and unchanged")
		return
	}
	request.RequestID = store.ID()
	if request.IdempotencyKey == "" {
		request.IdempotencyKey = store.ID()
	}
	d.status.SetText("Whirlwind | sending decision to local supervisor…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, err := d.commands(ctx, request)
		d.QueueUpdateDraw(func() {
			if err != nil {
				d.status.SetText("Whirlwind | command failed: " + tview.Escape(err.Error()))
			} else {
				d.status.SetText("Whirlwind | decision saved by supervisor")
			}
		})
	}()
}

func (d *Dashboard) showForm(form *tview.Form) {
	form.SetBorder(true).SetTitle(" Local supervisor decision ")
	form.AddButton("Cancel", d.closeModal)
	d.modal = form
	d.SetRoot(form, true).SetFocus(form)
}
