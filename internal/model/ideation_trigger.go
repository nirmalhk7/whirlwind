package model

import "encoding/json"

const RegistrationIdeationTrigger = "project_registration"

func (operation Operation) RegistrationIdeation() bool {
	if operation.Kind != "ideation" {
		return false
	}
	encoded, err := json.Marshal(operation.Input)
	if err != nil {
		return false
	}
	var input struct {
		Trigger   string `json:"trigger"`
		ProjectID string `json:"project_id"`
		Day       string `json:"day"`
		Fresh     *bool  `json:"fresh"`
	}
	if json.Unmarshal(encoded, &input) != nil {
		return false
	}
	if input.Trigger != "" {
		return input.Trigger == RegistrationIdeationTrigger
	}
	// Pre-marker, unstarted registrations had no owner and no explicit fresh
	// flag. Manual queue entries always recorded fresh; scheduled runs started
	// with an owner. Do not invent a trigger for ambiguous historical runs.
	return operation.Owner == "" && input.ProjectID != "" && input.ProjectID == operation.Subject && input.Day != "" && input.Fresh == nil
}
