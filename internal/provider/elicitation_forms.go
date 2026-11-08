package provider

import (
	"errors"
	"fmt"
	"sort"
	"whirlwind/internal/model"
)

type elicitationForm struct {
	fields    map[string]map[string]any
	questions []model.Question
	required  map[string]bool
}

func elicitationQuestions(schema any) (map[string]map[string]any, []model.Question, map[string]bool, error) {
	properties, required, err := elicitationSchema(schema)
	if err != nil {
		return nil, nil, nil, err
	}
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	fields := map[string]map[string]any{}
	questions := []model.Question{}
	for _, name := range names {
		field, ok := properties[name].(map[string]any)
		if !ok || name == "" {
			return nil, nil, nil, errors.New("MCP form properties must be primitive schemas")
		}
		question, err := elicitationQuestion(name, field, required[name])
		if err != nil {
			return nil, nil, nil, err
		}
		fields[name], questions = field, append(questions, question)
	}
	return fields, questions, required, nil
}

func elicitationSchema(schema any) (map[string]any, map[string]bool, error) {
	object, ok := schema.(map[string]any)
	if !ok || object["type"] != "object" {
		return nil, nil, errors.New("MCP form requires an object schema")
	}
	if err := validateElicitationObject(object); err != nil {
		return nil, nil, err
	}
	properties, ok := object["properties"].(map[string]any)
	if !ok || len(properties) == 0 {
		return nil, nil, errors.New("MCP form requires primitive properties")
	}
	required, err := elicitationRequired(object, properties)
	if err != nil {
		return nil, nil, err
	}
	return properties, required, nil
}

func validateElicitationObject(object map[string]any) error {
	for key := range object {
		switch key {
		case "type", "properties", "required", "title", "description", "$schema", "additionalProperties":
		default:
			return errors.New("unsupported MCP form schema keyword")
		}
	}
	if value, exists := object["additionalProperties"]; exists {
		if _, ok := value.(bool); !ok {
			return errors.New("nested MCP additional-properties schemas are unsupported")
		}
	}
	return nil
}

func elicitationRequired(object, properties map[string]any) (map[string]bool, error) {
	required := map[string]bool{}
	raw, exists := object["required"]
	if !exists {
		return required, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, errors.New("MCP required fields must be an array")
	}
	for _, value := range list {
		name, ok := value.(string)
		if !ok || properties[name] == nil || required[name] {
			return nil, errors.New("MCP required fields must name unique properties")
		}
		required[name] = true
	}
	return required, nil
}

func elicitationQuestion(name string, field map[string]any, required bool) (model.Question, error) {
	question := model.Question{ID: name, Text: name}
	if err := validateElicitationProperty(field); err != nil {
		return question, err
	}
	if title, _ := field["title"].(string); title != "" {
		question.Text = title
	}
	if description, _ := field["description"].(string); description != "" {
		question.Text += ": " + description
	}
	question.Text += fmt.Sprintf(" (%s", field["type"])
	if required {
		question.Text += ", required)"
	} else {
		question.Text += ", optional)"
	}
	format, _ := field["format"].(string)
	question.Secret = format == "password" || format == "secret"
	if format != "" && !question.Secret {
		return question, errors.New("unsupported MCP property format")
	}
	if rawEnum, exists := field["enum"]; exists {
		options, err := elicitationOptions(rawEnum)
		if err != nil {
			return question, err
		}
		question.Options = options
	}
	return question, nil
}

func validateElicitationProperty(field map[string]any) error {
	for key := range field {
		switch key {
		case "type", "title", "description", "enum", "format", "default":
		default:
			return errors.New("unsupported MCP property schema keyword")
		}
	}
	switch field["type"] {
	case "string", "number", "integer", "boolean":
	default:
		return errors.New("nested or unsupported MCP form schema; only primitive fields are supported")
	}
	return nil
}
