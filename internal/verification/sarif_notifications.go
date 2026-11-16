package verification

import (
	"encoding/json"
	"errors"
)

var errSARIFNotification = errors.New("baseline comparison blocked: invalid SARIF notification")

type baselineSARIFNotification struct {
	Level   json.RawMessage `json:"level"`
	Message json.RawMessage `json:"message"`
}

type baselineSARIFNotifications []baselineSARIFNotification

func (notifications *baselineSARIFNotifications) UnmarshalJSON(data []byte) error {
	type decodedNotifications baselineSARIFNotifications
	var decoded decodedNotifications
	if err := json.Unmarshal(data, &decoded); err != nil || decoded == nil {
		return errSARIFNotification
	}
	*notifications = baselineSARIFNotifications(decoded)
	return nil
}

func (notification baselineSARIFNotification) validate() error {
	if !validSARIFNotificationMessage(notification.Message) {
		return errSARIFNotification
	}
	// SARIF defaults an omitted notification level to warning. An explicitly
	// null, empty or unknown level does not carry that default.
	level := "warning"
	if len(notification.Level) > 0 {
		var valid bool
		level, valid = sarifNotificationString(notification.Level)
		if !valid {
			return errSARIFNotification
		}
	}
	switch level {
	case "none", "note", "warning":
		return nil
	case "error":
		return errors.New("baseline comparison blocked: SARIF tool error")
	default:
		return errSARIFNotification
	}
}

func validSARIFNotificationMessage(raw json.RawMessage) bool {
	var message map[string]json.RawMessage
	if json.Unmarshal(raw, &message) != nil || message == nil {
		return false
	}
	_, text := message["text"]
	_, id := message["id"]
	if !text && !id {
		return false
	}
	for field, value := range message {
		if !validSARIFMessageField(field, value) {
			return false
		}
	}
	return true
}

func validSARIFMessageField(field string, value json.RawMessage) bool {
	switch field {
	case "text", "id", "markdown":
		_, valid := sarifNotificationString(value)
		return valid
	case "arguments":
		var arguments []json.RawMessage
		if json.Unmarshal(value, &arguments) != nil || arguments == nil {
			return false
		}
		for _, argument := range arguments {
			if _, valid := sarifNotificationString(argument); !valid {
				return false
			}
		}
		return true
	case "properties":
		var properties map[string]json.RawMessage
		return json.Unmarshal(value, &properties) == nil && properties != nil
	default:
		return false
	}
}

func sarifNotificationString(raw json.RawMessage) (string, bool) {
	var value *string
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return "", false
	}
	return *value, true
}
