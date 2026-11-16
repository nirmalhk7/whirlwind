package dashboard

import (
	"encoding/json"
	"whirlwind/internal/model"
)

func (d *Dashboard) confirmDirection() {
	index := d.directions.GetCurrentItem()
	if index < 0 || index >= len(d.directionRows) {
		return
	}
	direction := d.directionRows[index]
	version := d.directionRecordVersion(direction.ID)
	d.confirmDirectionRecord(direction, version)
}

func (d *Dashboard) confirmDirectionRecord(direction model.Direction, version int) {
	if idea, current, ok := d.ideaRecord(direction.ID); ok {
		d.showIdeaRecord(idea, current)
		return
	}
	action, reason, ok := d.editDecision("# Proposal: "+direction.Title+"\n\n"+messageText(direction), []string{"approve", "reject"})
	if !ok || action == "cancel" {
		return
	}
	if action == "approve" && reason != "" {
		d.status.SetText("Whirlwind | invalid decision: proposal reason applies to reject only")
		return
	}
	request := directionApprovalRequest(direction, version)
	if action == "reject" {
		request.Operation = "direction.reject"
		request.Payload, _ = json.Marshal(map[string]string{"reason": reason})
	}
	d.closeModal()
	d.submit(request)
}

func (d *Dashboard) directionRecordVersion(id string) int {
	if _, version, ok := d.ideaRecord(id); ok {
		return version
	}
	for _, record := range d.snapshot["directions"] {
		if record.ID == id {
			return record.Version
		}
	}
	return 0
}
