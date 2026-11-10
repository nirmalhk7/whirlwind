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
	projects       *tview.List
	directions     *tview.List
	inbox          *tview.List
	inboxIdeaCount int
	inboxTaskIDs   []string
	plans          *tview.List
	runs           *tview.List
	runRows        []runRow
	runID          string
	taskView       bool
	detail         *tview.TextView
	status         *tview.TextView
	agents         *tview.TextView
	focus          int
	snapshot       control.Snapshot
	root           tview.Primitive
	modal          tview.Primitive
	commands       func(context.Context, control.Request) (json.RawMessage, error)
	projectRows    []model.Project
	projectHealth  map[string]model.ProjectStatus
	directionRows  []model.Direction
	planRows       []model.Task
	projectID      string
	directionID    string
	planID         string
	updating       bool
	dataDirectory  string
	inboxMessages  []string
}

func New(dataDirectories ...string) *Dashboard {
	d := &Dashboard{Application: tview.NewApplication(), projects: tview.NewList().ShowSecondaryText(false), directions: tview.NewList().ShowSecondaryText(false), plans: tview.NewList().ShowSecondaryText(false), detail: tview.NewTextView().SetScrollable(true)}
	d.dataDirectory = os.Getenv("WHIRLWIND_DATA_DIR")
	if len(dataDirectories) > 0 {
		d.dataDirectory = dataDirectories[0]
	}
	if d.dataDirectory == "" {
		home, _ := os.UserHomeDir()
		d.dataDirectory = filepath.Join(home, ".local", "share", "whirlwind")
	}
	d.EnableMouse(true)
	d.inbox = tview.NewList().ShowSecondaryText(false).SetHighlightFullLine(true).SetSelectedBackgroundColor(tcell.ColorWhite).SetSelectedTextColor(tcell.ColorBlack)
	d.inbox.SetMouseCapture(func(action tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
		if action == tview.MouseLeftClick {
			x, y := event.Position()
			left, top, width, height := d.inbox.GetInnerRect()
			offset, _ := d.inbox.GetOffset()
			if x >= left && x < left+width && y >= top && y < top+height {
				index := offset + y - top
				if index < len(d.inboxMessages) {
					d.inbox.SetCurrentItem(index)
					d.focus = 3
					d.SetFocus(d.inbox)
					d.viewMessage(index)
					return action, nil
				}
			}
		}
		return action, event
	})
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
	d.agents.SetBorder(true).SetTitle(" Agents ")
	d.agents.SetText("No configured agents")
	d.projects.AddItem("No projects", "", 0, nil)
	d.directions.AddItem("No inbox items", "", 0, nil)
	d.plans.AddItem("No plans", "", 0, nil)
	d.detail.SetText("Select a project, then a run to inspect agent history.")
	d.status = tview.NewTextView().SetText("Whirlwind | q quit | x stop/resume runners | Tab panels | Inbox: a approve, h history, t tasks | Runs/Tasks: Enter details, r review, c Qs, p perms, f final, l logs")
	navigation := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(d.projects, 0, 1, true).AddItem(d.agents, 0, 1, false).AddItem(&runListView{d.runs}, 0, 1, false)
	d.detail.SetBorder(true).SetTitle(inboxTitle)
	panels := tview.NewFlex().AddItem(navigation, 0, 3, true).AddItem(d.inbox, 0, 10, false)
	help := tview.NewTextView().SetText("q quit | Tab panels | Enter open | v/click Vim | l/click run logs | a approve | d delete | ? all keys")
	d.root = tview.NewFlex().SetDirection(tview.FlexRow).AddItem(panels, 0, 1, true).AddItem(d.status, 1, 0, false).AddItem(help, 1, 0, false)

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

func (d *Dashboard) SetCommandHandler(handler func(context.Context, control.Request) (json.RawMessage, error)) {
	d.commands = handler
}

func (d *Dashboard) input(event *tcell.EventKey) *tcell.EventKey {
	if event.Key() == tcell.KeyCtrlC {
		d.Stop()
		return nil
	}
	if d.modal != nil {
		return d.modalInput(event)
	}
	if event.Rune() == 'q' {
		d.Stop()
		return nil
	}
	if d.focusPanel(event) {
		return nil
	}
	if d.handlePanelAction(event) {
		return nil
	}
	return event
}

func (d *Dashboard) modalInput(event *tcell.EventKey) *tcell.EventKey {
	if event.Key() != tcell.KeyEscape {
		return event
	}
	d.closeModal()
	return nil
}

func (d *Dashboard) focusPanel(event *tcell.EventKey) bool {
	if event.Key() != tcell.KeyTab && event.Key() != tcell.KeyBacktab {
		return false
	}
	order := []int{0, 4, 2, 3}
	position := 0
	for i, panel := range order {
		if panel == d.focus {
			position = i
		}
	}
	step := 1
	if event.Key() == tcell.KeyBacktab {
		step = len(order) - 1
	}
	d.focus = order[(position+step)%len(order)]
	d.SetFocus(d.focusedPanel())
	return true
}

func (d *Dashboard) focusedPanel() tview.Primitive {
	return []tview.Primitive{d.projects, d.directions, d.runs, d.inbox, d.agents}[d.focus]
}

func (d *Dashboard) handlePanelAction(event *tcell.EventKey) bool {
	switch {
	case d.openRunDetails(event):
	case event.Rune() == '?':
		d.showKeyboardHelp()
	case event.Rune() == 'v' && d.focus == 3:
		d.viewMessage(d.inbox.GetCurrentItem())
	case event.Rune() == 'd' && d.focus == 3:
		d.deleteInboxProposal()
	case event.Rune() == 'x':
		d.submit(control.Request{Operation: "runners.toggle"})
	case event.Rune() == 'a' && d.focus == 3:
		d.chooseDirection()
	case event.Rune() == 'a' && d.focus == 1:
		d.confirmDirection()
	case event.Rune() == 'h' && (d.focus == 1 || d.focus == 3):
		d.reconsiderDirectionHistory()
	case event.Rune() == 't' && d.focus == 3:
		d.chooseTask()
	case event.Rune() == 'r' && (d.focus == 2 || d.focus == 3):
		d.reviewPlanForm()
	case event.Rune() == 'c' && (d.focus == 2 || d.focus == 3):
		d.answerQuestionsForm()
	case event.Rune() == 'p' && (d.focus == 2 || d.focus == 3):
		d.permissionForm()
	case event.Rune() == 'f' && (d.focus == 2 || d.focus == 3):
		d.finalizationView()
	case event.Rune() == 'l' && d.focus == 2:
		d.showRunLogs(d.runs.GetCurrentItem())
	case event.Rune() == 'l' && d.focus == 3:
		d.loadLatestLog()
	default:
		return false
	}
	return true
}

func (d *Dashboard) closeModal() {
	if d.modal == d.directions {
		d.directions.SetTitle(inboxTitle)
	}
	d.modal = nil
	d.SetRoot(d.root, true)
	d.SetFocus(d.focusedPanel())
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

func (d *Dashboard) openRunDetails(event *tcell.EventKey) bool {
	if event.Key() != tcell.KeyEnter || d.focus != 2 || len(d.runRows) == 0 {
		return false
	}
	d.showRunLogs(d.runs.GetCurrentItem())
	return true
}
