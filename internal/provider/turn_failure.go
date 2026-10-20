package provider

import (
	"encoding/json"
	"fmt"
)

// codexTurnFailure preserves the failed turn's original metadata and upstream HTTP status.
func codexTurnFailure(raw json.RawMessage) error {
	var value struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Info    json.RawMessage `json:"codexErrorInfo"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("codex turn failed: %s: %w", raw, err)
	}
	status := turnHTTPStatus(value.Info)
	if status == 0 {
		status = value.Code
	}
	return &RPCError{Code: status, Message: "Codex turn failed: " + value.Message, Data: raw}
}

func turnHTTPStatus(info json.RawMessage) int {
	var variants map[string]struct {
		Status int `json:"httpStatusCode"`
	}
	if json.Unmarshal(info, &variants) != nil {
		return 0
	}
	for _, key := range []string{"httpConnectionFailed", "responseStreamConnectionFailed", "responseStreamDisconnected", "responseTooManyFailedAttempts"} {
		if variants[key].Status != 0 {
			return variants[key].Status
		}
	}
	return 0
}
