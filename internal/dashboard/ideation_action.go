package dashboard

import (
	"encoding/json"

	"whirlwind/internal/control"
)

func (d *Dashboard) startProjectIdeation() {
	if d.projectID == "" {
		d.status.SetText("Whirlwind | select a project before starting ideation")
		return
	}
	for _, record := range d.snapshot["settings"] {
		if record.ID != "global" {
			continue
		}
		var settings struct {
			Stopped bool `json:"stopped"`
		}
		if err := json.Unmarshal(record.Data, &settings); err != nil {
			d.status.SetText("Whirlwind | runner status unreadable; refresh before starting ideation")
			return
		}
		if settings.Stopped {
			d.status.SetText("Whirlwind | runners stopped; press x to resume before starting ideation")
			return
		}
	}
	if !d.hasCommandHandler("Whirlwind | local supervisor unavailable; ideation was not started") {
		return
	}
	payload, _ := json.Marshal(map[string]bool{"fresh": true})
	for _, record := range d.snapshot["projects"] {
		if record.ID == d.projectID && record.Version > 0 {
			d.submit(control.Request{Operation: "ideation.start", ProjectID: d.projectID, ExpectedVersion: record.Version, Payload: payload})
			return
		}
	}
	d.status.SetText("Whirlwind | registered project version unavailable; refresh before starting ideation")
}
