package model

import "encoding/json"

const RegistrationIdeationTrigger = "project_registration"

func (operation Operation) RegistrationIdeation() bool {
	trigger, registrationInput := operation.ideationTrigger()
	if trigger != "" {
		return trigger == RegistrationIdeationTrigger
	}
	return registrationInput && operation.Owner == ""
}

// HasLegacyRegistrationInput identifies queued input written before triggers
// were recorded. The original operation event determines whether it was automatic.
func (operation Operation) HasLegacyRegistrationInput() bool {
	trigger, registrationInput := operation.ideationTrigger()
	return trigger == "" && registrationInput
}

func (operation Operation) ideationTrigger() (string, bool) {
	if operation.Kind != "ideation" {
		return "", false
	}
	encoded, err := json.Marshal(operation.Input)
	if err != nil {
		return "", false
	}
	var input struct {
		Trigger   string `json:"trigger"`
		ProjectID string `json:"project_id"`
		Day       string `json:"day"`
		Fresh     *bool  `json:"fresh"`
	}
	if json.Unmarshal(encoded, &input) != nil {
		return "", false
	}
	return input.Trigger, input.ProjectID != "" && input.ProjectID == operation.Subject && input.Day != "" && input.Fresh == nil
}

// RecordRegistrationIdeationTrigger preserves the trigger when an operation
// loaded from storage acquires a worker or is requeued after a lease conflict.
func (operation *Operation) RecordRegistrationIdeationTrigger() bool {
	input, ok := operation.Input.(map[string]any)
	if !ok || input["trigger"] == RegistrationIdeationTrigger {
		return false
	}
	input["trigger"] = RegistrationIdeationTrigger
	return true
}
