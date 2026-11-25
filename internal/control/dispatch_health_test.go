package control

import (
	"errors"
	"testing"

	"whirlwind/internal/model"
)

func TestDispatchHealthInspectionReportsSupervisorStateWithoutMutation(t *testing.T) {
	state := model.DispatchHealth{Checked: true, BlockReason: "sandbox authority is unverified"}
	dispatch := SupervisorDispatcher{DispatchHealth: func() model.DispatchHealth { return state }}
	request := Request{RequestID: "health", Operation: DispatchHealthOperation}
	if err := validateRequest(request); err != nil {
		t.Fatalf("read-only health inspection required mutation parameters: %v", err)
	}
	response, err := dispatch.Handle(t.Context(), request)
	got, ok := response.Result.(model.DispatchHealth)
	if err != nil || !ok || got != state || response.RequestID != request.RequestID {
		t.Fatalf("health result=%+v, err=%v", response, err)
	}
	if _, err := (SupervisorDispatcher{}).Handle(t.Context(), request); !errors.Is(err, ErrDispatchHealthUnavailable) {
		t.Fatalf("older dispatcher was not handled explicitly: %v", err)
	}
}
