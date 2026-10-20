package httpapi

import (
	"encoding/json"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (q apiRequest) directionGroup(direction model.Direction) ([]model.Record, error) {
	records, err := q.Store.List(q.r.Context(), "directions")
	if err != nil {
		return nil, err
	}
	group := []model.Record{}
	for _, record := range records {
		d, err := store.Decode[model.Direction](record)
		if err != nil {
			return nil, err
		}
		if record.ID == direction.ID || (direction.GroupID != "" && d.GroupID == direction.GroupID && d.ProjectID == direction.ProjectID) {
			group = append(group, record)
		}
	}
	return group, nil
}

func (q apiRequest) unselectGroupPeers(direction model.Direction) error {
	group, err := q.directionGroup(direction)
	if err != nil {
		return err
	}
	for _, record := range group {
		if record.ID == direction.ID {
			continue
		}
		d, err := store.Decode[model.Direction](record)
		if err != nil {
			return err
		}
		if d.Selected {
			if err = q.pauseDirectionTask("direction-" + record.ID); err != nil {
				return err
			}
		}
		d.Selected = false
		// Touch every peer, including unselected ones: competing selections from
		// the same group must conflict instead of committing two selected ideas.
		if err = q.Store.Put(q.r.Context(), "directions", record.ID, d, record.Version); err != nil {
			return err
		}
	}
	return nil
}

func (q apiRequest) deleteDirectionGroup() error {
	group, versions, err := q.directionGroupForDeletion()
	if err != nil {
		return err
	}
	if err := validateDirectionGroupVersions(group, versions); err != nil {
		return err
	}
	if err := q.removeDirectionGroup(group); err != nil {
		return err
	}
	reply(q.w, 200, map[string]int{"deleted": len(group)})
	return nil
}

func (q apiRequest) directionGroupForDeletion() ([]model.Record, map[string]int, error) {
	direction, _, err := readCurrent[model.Direction](q, "directions")
	if err != nil {
		return nil, nil, err
	}
	var input struct {
		Versions map[string]int `json:"versions"`
	}
	if err = json.Unmarshal(q.body, &input); err != nil {
		return nil, nil, &apiError{422, "invalid_input", "Idea versions are required"}
	}
	group, err := q.directionGroup(direction)
	if err != nil {
		return nil, nil, err
	}
	return group, input.Versions, nil
}

func validateDirectionGroupVersions(group []model.Record, versions map[string]int) error {
	if len(versions) != len(group) {
		return store.ErrConflict
	}
	for _, record := range group {
		if versions[record.ID] != record.Version {
			return store.ErrConflict
		}
	}
	return nil
}

func (q apiRequest) removeDirectionGroup(group []model.Record) error {
	for _, record := range group {
		if err := q.pauseDirectionTask("direction-" + record.ID); err != nil {
			return err
		}
		if err := q.Store.RemoveDirection(q.r.Context(), record.ID, record.Version); err != nil {
			return err
		}
	}
	return nil
}
