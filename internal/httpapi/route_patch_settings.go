package httpapi

import (
	"encoding/json"
	"errors"
	"whirlwind/internal/store"
	"whirlwind/internal/workflow"
)

func (q apiRequest) patchSettings() error {
	n, e := expected(q.r)
	if q.r.Header.Get("If-Match") == "0" {
		n, e = 0, nil
	}
	if e != nil {
		return &apiError{428, "precondition_required", e.Error()}
	}
	v := workflow.DefaultSettings()
	if err := q.loadSettingsVersion(n, &v); err != nil {
		return err
	}
	if err := q.applySettingsPatch(&v); err != nil {
		return err
	}
	q.save(q.w, q.r, "settings", "global", v, n, 200)
	return nil
}

// loadSettingsVersion checks the requested version before decoding persisted settings.
func (q apiRequest) loadSettingsVersion(n int, v *workflow.Settings) error {
	if record, err := q.Store.Get(q.r.Context(), "settings", "global"); err == nil {
		if n != record.Version {
			return store.ErrConflict
		}
		return json.Unmarshal(record.Data, v)
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	} else if n != 0 {
		return store.ErrConflict
	}
	return nil
}

// applySettingsPatch merges the request over current defaults and validates the result.
func (q apiRequest) applySettingsPatch(v *workflow.Settings) error {
	if err := json.Unmarshal(q.body, v); err != nil {
		return err
	}
	if err := v.Ideation.Validate(); err != nil {
		return &apiError{422, "invalid_input", err.Error()}
	}
	if !settingsValid(*v) {
		return &apiError{422, "invalid_input", "slots must be 1..32; reconciliation_seconds 30..86400"}
	}
	return nil
}

func settingsValid(v workflow.Settings) bool {
	slotsValid := v.GlobalSlots >= 1 && v.GlobalSlots <= 32 && v.ProjectSlots >= 1 && v.ProjectSlots <= 32
	return slotsValid && v.ReconciliationSeconds >= 30 && v.ReconciliationSeconds <= 86400
}
