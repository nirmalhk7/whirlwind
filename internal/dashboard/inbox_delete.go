package dashboard

import "whirlwind/internal/control"

func (d *Dashboard) deleteInboxProposal() {
	index := d.inbox.GetCurrentItem()
	if index < 0 || index >= len(d.directionRows) {
		d.status.SetText("Whirlwind | highlight an ideation proposal in Inbox to delete it")
		return
	}
	direction := d.directionRows[index]
	if idea, version, ok := d.ideaRecord(direction.ID); ok {
		if idea.DecisionStatus == "in_process" {
			d.status.SetText("Whirlwind | active planning must finish or be cancelled before archival")
			return
		}
		d.archiveIdeaForm(idea, version)
		return
	}
	version := d.directionRecordVersion(direction.ID)
	if version < 1 {
		d.status.SetText("Whirlwind | proposal version unavailable; refresh before deleting")
		return
	}
	d.submit(control.Request{Operation: "direction.delete", ResourceID: direction.ID, ProjectID: direction.ProjectID, ExpectedVersion: version})
}
