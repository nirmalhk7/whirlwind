package provider

import (
	"encoding/json"
	"errors"
	"strings"
)

// JSON extracts the outer JSON object or array from agent text and unmarshals it into the supplied destination.
func JSON(text string, v any) error {
	text = strings.TrimSpace(text)
	if json.Unmarshal([]byte(text), v) == nil {
		return nil
	}
	start := strings.IndexAny(text, "{[")
	if start < 0 {
		return errors.New("agent must return JSON")
	}
	dec := json.NewDecoder(strings.NewReader(text[start:]))
	return dec.Decode(v)
}
