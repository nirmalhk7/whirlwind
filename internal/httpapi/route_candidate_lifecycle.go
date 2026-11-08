package httpapi

import (
	"encoding/json"
	"strings"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (q apiRequest) candidateLifecycle() error {
	d, record, err := readCurrent[model.Direction](q, "directions")
	if err != nil {
		return err
	}
	saved, err := q.updateCandidateLifecycle(d, record)
	if err != nil {
		return err
	}
	reply(q.w, 200, saved)
	return nil
}

func (q apiRequest) updateCandidateLifecycle(d model.Direction, record model.Record) (any, error) {
	switch q.action {
	case "reconsider":
		return q.requestCandidateReconsideration(d, record)
	case "dismiss":
		d.Status = "dismissed"
	case "defer":
		d.Status = "deferred"
	case "reopen":
		d.Status = "proposed"
	default:
		return nil, &apiError{404, "route_not_found", "Unknown candidate action"}
	}
	if d.Selected {
		if err := q.pauseDirectionTask(directionTask(d)); err != nil {
			return nil, err
		}
	}
	d.Selected = false
	if err := q.Store.Put(q.r.Context(), "directions", d.ID, d, record.Version); err != nil {
		return nil, err
	}
	return q.Store.Get(q.r.Context(), "directions", d.ID)
}

func (q apiRequest) requestCandidateReconsideration(direction model.Direction, record model.Record) (any, error) {
	var input struct {
		ProjectID string `json:"project_id"`
		Reason    string `json:"reason"`
	}
	if err := json.Unmarshal(q.body, &input); err != nil {
		return nil, &apiError{422, "invalid_input", "Reconsideration requires the project ID and human reason"}
	}
	if input.ProjectID == "" || input.ProjectID != direction.ProjectID {
		return nil, &apiError{409, "project_mismatch", "Reconsideration project does not match the saved direction"}
	}
	if strings.TrimSpace(input.Reason) == "" {
		return nil, &apiError{422, "invalid_input", "A human reason is required to reconsider this proposal"}
	}
	version, err := expected(q.r)
	if err != nil {
		return nil, &apiError{428, "precondition_required", err.Error()}
	}
	if version != record.Version {
		return nil, store.ErrConflict
	}
	if direction.Selected || (direction.Status != "dismissed" && direction.Status != "rejected") {
		return nil, &apiError{409, "candidate_not_suppressed", "Only a dismissed or rejected, unselected direction can be reconsidered"}
	}
	authorization := model.ProposalReconsideration{
		ID:                        store.ID(),
		ProjectID:                 direction.ProjectID,
		PredecessorID:             direction.ID,
		Reason:                    strings.TrimSpace(input.Reason),
		Status:                    "pending",
		RequestedDirectionVersion: record.Version + 1,
	}
	if err := q.Store.Put(q.r.Context(), "proposal_reconsiderations", authorization.ID, authorization, 0); err != nil {
		return nil, err
	}
	direction.Status = "reconsider_requested"
	if err := q.Store.Put(q.r.Context(), "directions", direction.ID, direction, record.Version); err != nil {
		return nil, err
	}
	return q.Store.Get(q.r.Context(), "directions", direction.ID)
}
