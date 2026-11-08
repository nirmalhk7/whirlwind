package dashboard

func (d *Dashboard) chooseDirection() {
	if len(d.directionRows) == 0 {
		d.status.SetText("Whirlwind | no directions awaiting approval for this project")
		return
	}
	d.modal = d.directions
	d.directions.SetTitle(" Choose direction · Enter to approve · Esc to close ")
	d.SetRoot(d.directions, true).SetFocus(d.directions)
}
