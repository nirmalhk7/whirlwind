package dashboard

import (
	"encoding/json"

	"whirlwind/internal/control"
)

func (d *Dashboard) toggleRunners() {
	if !d.hasCommandHandler("Whirlwind | local supervisor unavailable; runner state unchanged") {
		return
	}
	for _, record := range d.snapshot["settings"] {
		if record.ID != "global" {
			continue
		}
		var settings struct {
			Stopped bool `json:"stopped"`
		}
		if err := json.Unmarshal(record.Data, &settings); err != nil || record.Version < 1 {
			break
		}
		payload, _ := json.Marshal(map[string]bool{"stopped": !settings.Stopped})
		d.submit(control.Request{Operation: "runners.toggle", ResourceID: "global", ExpectedVersion: record.Version, Payload: payload})
		return
	}
	d.status.SetText("Whirlwind | runner settings unavailable; refresh before changing state")
}
