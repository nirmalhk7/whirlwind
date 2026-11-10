package httpapi

import "whirlwind/internal/model"

// Deleting one proposal preserves its durable ledger and any run evidence.
func (q apiRequest) deleteDirection() error {
	direction, record, err := readCurrent[model.Direction](q, "directions")
	if err != nil {
		return err
	}
	if err := q.pauseDirectionTask(directionTask(direction)); err != nil {
		return err
	}
	if err := q.Store.RemoveDirection(q.r.Context(), record.ID, record.Version); err != nil {
		return err
	}
	reply(q.w, 200, map[string]int{"deleted": 1})
	return nil
}
