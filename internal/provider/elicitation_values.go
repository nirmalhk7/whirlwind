package provider

import (
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
)

func elicitationOptions(raw any) ([]string, error) {
	options, ok := raw.([]any)
	if !ok || len(options) == 0 {
		return nil, errors.New("MCP form enum must contain primitive options")
	}
	labels := []string{}
	for _, value := range options {
		switch value.(type) {
		case string, float64, bool:
		default:
			return nil, errors.New("MCP form enum options must be primitives")
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, errors.New("invalid MCP form enum option")
		}
		label := string(encoded)
		if text, ok := value.(string); ok {
			label = text
		}
		labels = append(labels, label)
	}
	return labels, nil
}

func elicitationValue(field map[string]any, answer string) (any, error) {
	value, err := parseElicitationValue(field["type"], answer)
	if err != nil {
		return nil, err
	}
	if err = validateElicitationEnum(field, value); err != nil {
		return nil, err
	}
	return value, nil
}

func parseElicitationValue(kind any, answer string) (any, error) {
	if kind == "string" {
		return answer, nil
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(answer))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, errors.New("answer must use its declared JSON type")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("answer must contain exactly one JSON value")
	}
	if err := validateElicitationType(kind, value); err != nil {
		return nil, err
	}
	return value, nil
}

func validateElicitationType(kind, value any) error {
	switch kind {
	case "boolean":
		if _, ok := value.(bool); !ok {
			return errors.New("answer must be true or false")
		}
	case "integer", "number":
		return validateElicitationNumber(kind, value)
	}
	return nil
}

func validateElicitationNumber(kind, value any) error {
	number, ok := value.(json.Number)
	if !ok {
		return errors.New("answer must be numeric")
	}
	if kind == "integer" {
		if _, err := strconv.ParseInt(string(number), 10, 64); err != nil {
			return errors.New("answer must be an integer")
		}
	}
	return nil
}

func validateElicitationEnum(field map[string]any, value any) error {
	options, ok := field["enum"].([]any)
	if !ok {
		return nil
	}
	encoded, _ := json.Marshal(value)
	for _, option := range options {
		candidate, _ := json.Marshal(option)
		if string(candidate) == string(encoded) {
			return nil
		}
	}
	return errors.New("answer must match a requested enum option")
}
