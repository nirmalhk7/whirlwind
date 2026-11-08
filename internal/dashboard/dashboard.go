// Package dashboard presents local supervisor state in the terminal.
package dashboard

import (
	"context"
	"encoding/json"
	"time"
	"whirlwind/internal/control"
	"whirlwind/internal/model"
	"whirlwind/internal/store"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type Dashboard struct {
	*tview.Application
	projects      *tview.List
	directions    *tview.List
	plans         *tview.List
	detail        *tview.TextView
	status        *tview.TextView
	agents        *tview.TextView
	focus         int
	snapshot      control.Snapshot
	root          tview.Primitive
	modal         tview.Primitive
	commands      func(context.Context, control.Request) (json.RawMessage, error)
	projectRows   []model.Project
	projectHealth map[string]model.ProjectStatus
	directionRows []model.Direction
	planRows      []model.Task
	projectID     string
	directionID   string
	planID        string
	updating      bool
}

func New() *Dashboard {
	d := &Dashboard{Application: tview.NewApplication(), projects: tview.NewList().ShowSecondaryText(false), directions: tview.NewList().ShowSecondaryText(false), plans: tview.NewList().ShowSecondaryText(false), detail: tview.NewTextView().SetScrollable(true)}
	d.projects.SetBorder(true).SetTitle(" Projects ")
	d.directions.SetBorder(true).SetTitle(" Directions ")
	d.plans.SetBorder(true).SetTitle(" Plans ")
	d.agents = tview.NewTextView().SetDynamicColors(true).SetScrollable(true)
	d.agents.SetBorder(true).SetTitle(" Agents ")
	d.agents.SetText("No configured agents")
	d.projects.AddItem("No projects", "", 0, nil)
	d.directions.AddItem("No directions", "", 0, nil)
	d.plans.AddItem("No plans", "", 0, nil)
	d.detail.SetText("Select a project, then a plan to inspect durable execution state.")
	header := tview.NewTextView().SetText("Whirlwind | local terminal dashboard")
	d.status = header
	footer := tview.NewTextView().SetText("Tab panels | arrows | Inbox: a approve, h history | Plans: r review, c Qs, p perms, f final, l logs | q quit")
	navigation := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(d.projects, 0, 1, true).AddItem(d.agents, 0, 2, false).AddItem(d.plans, 0, 2, false)
	d.detail.SetBorder(true).SetTitle(" Inbox ")
	panels := tview.NewFlex().AddItem(navigation, 42, 0, true).AddItem(d.detail, 0, 1, false)
	d.root = tview.NewFlex().SetDirection(tview.FlexRow).AddItem(header, 1, 0, false).AddItem(panels, 0, 1, true).AddItem(footer, 1, 0, false)
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
	return []tview.Primitive{d.projects, d.directions, d.plans, d.detail, d.agents}[d.focus]
}

func (d *Dashboard) handlePanelAction(event *tcell.EventKey) bool {
	switch {
	case event.Rune() == 'a' && d.focus == 3:
		d.chooseDirection()
	case event.Rune() == 'a' && d.focus == 1:
		d.confirmDirection()
	case event.Rune() == 'h' && (d.focus == 1 || d.focus == 3):
		d.reconsiderDirectionHistory()
	case event.Rune() == 'r' && d.focus == 2:
		d.reviewPlanForm()
	case event.Rune() == 'c' && d.focus == 2:
		d.answerQuestionsForm()
	case event.Rune() == 'p' && d.focus == 2:
		d.permissionForm()
	case event.Rune() == 'f' && d.focus == 2:
		d.finalizationView()
	case event.Rune() == 'l' && d.focus == 2:
		d.loadLatestLog()
	default:
		return false
	}
	return true
}

func (d *Dashboard) closeModal() {
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
