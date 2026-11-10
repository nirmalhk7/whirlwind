package dashboard

import (
	"github.com/rivo/tview"
	"strings"
	"whirlwind/internal/model"
)

func (d *Dashboard) refreshInbox() {
	selected := d.inbox.GetCurrentItem()
	ideaSelected := selected < d.inboxIdeaCount
	d.inboxIdeaCount = len(d.directionRows)
	if ideaSelected && d.directionID != "" {
		selected = d.directions.GetCurrentItem()
	}
	d.updating = true
	defer func() { d.updating = false }()
	d.inbox.Clear()
	d.inboxTaskIDs = nil
	d.inboxMessages = nil
	for i := range d.directionRows {
		label, _ := d.directions.GetItemText(i)
		d.inbox.AddItem(d.recordTimestamp("directions", d.directionRows[i].ID)+" · "+label, "", 0, nil)
		d.inboxTaskIDs = append(d.inboxTaskIDs, "")
		d.inboxMessages = append(d.inboxMessages, messageText(d.directionRows[i]))
	}
	notices, err := decodeRecords[model.Notification](d.snapshot["inbox"])
	if err != nil {
		d.inbox.AddItem("Cannot read inbox: "+tview.Escape(err.Error()), "", 0, nil)
		return
	}
	questions, err := decodeRecords[model.QuestionSet](d.snapshot["questions"])
	if err != nil {
		d.inbox.AddItem("Cannot read questions: "+tview.Escape(err.Error()), "", 0, nil)
		return
	}
	permissions, err := decodeRecords[inboxPermission](d.snapshot["permissions"])
	if err != nil {
		d.inbox.AddItem("Cannot read permissions: "+tview.Escape(err.Error()), "", 0, nil)
		return
	}
	d.addNotices(notices, questions, permissions)
	if selected < d.inbox.GetItemCount() {
		d.inbox.SetCurrentItem(selected)
	}
	if d.inbox.GetItemCount() == 0 {
		d.inbox.AddItem("No inbox items", "", 0, nil)
	}
}

func agentRoleLabel(role string) string {
	return map[string]string{"ideate": "Ideation", "plan": "Planner", "code": "Coding", "review": "Reviewer", "finalizer": "Finalizer"}[role]
}

func (d *Dashboard) addAttemptNotice(attemptID, taskID, message, fullMessage, timestamp string) {
	if attemptID == "" {
		return
	}
	for _, row := range d.runRows {
		if row.attempt.AttemptID == attemptID {
			if role := agentRoleLabel(row.attempt.Role); role != "" {
				d.inbox.AddItem(tview.Escape(timestamp+" · "+role+" - "+message), "", 0, nil)
				d.inboxTaskIDs = append(d.inboxTaskIDs, taskID)
				d.inboxMessages = append(d.inboxMessages, fullMessage)
			}
		}
	}
}

type inboxPermission struct {
	ID        string `json:"id"`
	Task      string `json:"task"`
	AttemptID string `json:"attempt_id"`
}

func (d *Dashboard) addNotices(notices []model.Notification, questions []model.QuestionSet, permissions []inboxPermission) {
	for i, notice := range notices {
		timestamp := savedInboxTimestamp(notice.FirstAt, notice.LastAt, d.snapshot["inbox"][i].UpdatedAt)
		if notice.Resolved {
			continue
		}
		if role := agentRoleLabel(notice.Role); role != "" && d.hasInboxTask(notice.TaskID) {
			d.inbox.AddItem(tview.Escape(timestamp+" · "+role+" - "+notice.Message), "", 0, nil)
			d.inboxTaskIDs = append(d.inboxTaskIDs, notice.TaskID)
			d.inboxMessages = append(d.inboxMessages, messageText(notice))
			continue
		}
		d.addLegacyNotice(notice, questions, permissions, timestamp)
	}
}

func (d *Dashboard) openInboxTask(taskID string) {
	for index, task := range d.planRows {
		if task.ID == taskID {
			d.plans.SetCurrentItem(index)
			d.selectPlan(index, "", "", 0)
			d.taskView = true
			d.detail.SetTitle(" Task details · Esc to close ")
			d.modal = d.detail
			d.SetRoot(d.detail, true).SetFocus(d.detail)
			return
		}
	}
}

func (d *Dashboard) hasInboxTask(id string) bool {
	for _, task := range d.planRows {
		if id != "" && task.ID == id {
			return true
		}
	}
	return false
}

func (d *Dashboard) addLegacyNotice(notice model.Notification, questions []model.QuestionSet, permissions []inboxPermission, timestamp string) {
	for _, permission := range permissions {
		if notice.Kind == "permission" && notice.TaskID == permission.Task && strings.HasSuffix(notice.Message, ": "+permission.ID) {
			d.addAttemptNotice(permission.AttemptID, notice.TaskID, notice.Message, messageText(notice), timestamp)
		}
	}
	for _, question := range questions {
		if notice.Kind == "question" && notice.TaskID == question.TaskID && strings.HasSuffix(notice.Message, ": "+question.ID) {
			d.addAttemptNotice(question.AttemptID, notice.TaskID, notice.Message, messageText(notice), timestamp)
		}
	}
}

func (d *Dashboard) openInboxSelection(index int) {
	if index < 0 || index >= len(d.inboxTaskIDs) {
		return
	}
	for i, task := range d.planRows {
		if task.ID == d.inboxTaskIDs[index] && task.Stage == "plan_approval" && task.PlanDocument != nil {
			d.plans.SetCurrentItem(i)
			d.selectPlan(i, "", "", 0)
			d.reviewPlanForm()
			return
		}
	}
	d.openInboxTask(d.inboxTaskIDs[index])
}
