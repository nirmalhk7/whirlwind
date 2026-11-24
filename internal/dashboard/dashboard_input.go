package dashboard

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

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
	if d.focusPanel(event) || d.handlePanelAction(event) {
		return nil
	}
	return event
}

func (d *Dashboard) modalInput(event *tcell.EventKey) *tcell.EventKey {
	if event.Rune() == 'v' && d.planReviewView != nil && d.planReviewView.HasFocus() {
		d.planReviewVim()
		return nil
	}
	if event.Rune() == 'a' && d.directionChoiceFromRationale {
		d.closeModal()
		d.chooseDirection()
		return nil
	}
	if event.Key() == tcell.KeyEscape {
		d.closeModal()
		return nil
	}
	if d.togglePlanReviewFocus(event) {
		return nil
	}
	return event
}

func (d *Dashboard) togglePlanReviewFocus(event *tcell.EventKey) bool {
	if d.planReviewView == nil || event.Key() != tcell.KeyF6 {
		return false
	}
	if d.planReviewForm.HasFocus() {
		d.SetFocus(d.planReviewView)
	} else {
		d.SetFocus(d.planReviewForm)
	}
	return true
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
	if d.openRunDetails(event) || d.handleGlobalPanelAction(event) {
		return true
	}
	switch d.focus {
	case 0:
		return d.handleProjectPanelAction(event)
	case 1:
		return d.handleDirectionPanelAction(event)
	case 2, 3:
		if d.focus == 2 && event.Rune() == 'r' {
			return d.retrySelectedProjectSetup()
		}
		return d.handlePlanPanelAction(event)
	default:
		return false
	}
}

func (d *Dashboard) handleGlobalPanelAction(event *tcell.EventKey) bool {
	switch event.Rune() {
	case '?':
		d.showKeyboardHelp()
	case 'x':
		d.toggleRunners()
	default:
		return false
	}
	return true
}

func (d *Dashboard) handleProjectPanelAction(event *tcell.EventKey) bool {
	if event.Rune() != 'i' {
		return false
	}
	d.startProjectIdeation()
	return true
}

func (d *Dashboard) handleDirectionPanelAction(event *tcell.EventKey) bool {
	switch event.Rune() {
	case 'a':
		d.confirmDirection()
	case 'h':
		d.reconsiderDirectionHistory()
	default:
		return false
	}
	return true
}

func (d *Dashboard) handlePlanPanelAction(event *tcell.EventKey) bool {
	if d.focus == 3 && d.handleInboxPanelAction(event) {
		return true
	}
	switch event.Rune() {
	case 'r':
		d.reviewPlanForm()
	case 'c':
		d.answerQuestionsForm()
	case 'p':
		d.permissionForm()
	case 'f':
		d.finalizationView()
	default:
		return false
	}
	return true
}

func (d *Dashboard) handleInboxPanelAction(event *tcell.EventKey) bool {
	switch event.Rune() {
	case 'v':
		d.viewMessage(d.inbox.GetCurrentItem())
	case 'd':
		d.deleteInboxProposal()
	case 'o':
		d.showEarlierDirections()
	case 'a':
		d.chooseDirection()
	case 'h':
		d.reconsiderDirectionHistory()
	case 't':
		d.chooseTask()
	default:
		return false
	}
	return true
}

func (d *Dashboard) closeModal() {
	panel := d.focusedPanel()
	if d.modal == d.directions {
		d.directions.SetTitle(inboxTitle)
	}
	d.modal = nil
	d.ideaDetailView = nil
	d.planReviewView = nil
	d.planReviewForm = nil
	d.planReviewVim = nil
	d.directionChoiceFromRationale = false
	d.SetRoot(d.root, true)
	d.SetFocus(panel)
}

func (d *Dashboard) openRunDetails(event *tcell.EventKey) bool {
	if event.Key() != tcell.KeyEnter || d.focus != 2 || len(d.runRows) == 0 {
		return false
	}
	d.showRunLogs(d.runs.GetCurrentItem())
	return true
}
