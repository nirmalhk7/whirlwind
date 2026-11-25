package dashboard

import (
	"encoding/json"
	"strings"

	"github.com/rivo/tview"

	"whirlwind/internal/control"
	"whirlwind/internal/model"
)

func snapshotDispatchHealth(snapshot control.Snapshot) (model.DispatchHealth, bool) {
	for _, record := range snapshot[control.DispatchHealthSnapshotKey] {
		var state model.DispatchHealth
		if record.ID == "local" {
			// Invalid health data is unknown, not a green status.
			if json.Unmarshal(record.Data, &state) != nil {
				return model.DispatchHealth{}, true
			}
			return state, true
		}
	}
	return model.DispatchHealth{}, false
}

func dispatchBlockSummary(reason string) string {
	return strings.TrimPrefix(reason, "sandbox error: isolation could not be verified: ")
}

func (d *Dashboard) showDispatchBlock() bool {
	state, _ := snapshotDispatchHealth(d.snapshot)
	if state.BlockReason == "" || !supervisorOnline(d.snapshot) {
		return false
	}
	text := "Last observed dispatch block:\n\n" + state.BlockReason +
		"\n\nNew dispatch is held until sandbox recovery is verified."
	view := tview.NewTextView().SetScrollable(true).SetText(text)
	view.SetBorder(true).SetTitle(" Dispatch block details · Esc to close ")
	d.modal = view
	d.SetRoot(view, true).SetFocus(view)
	return true
}
