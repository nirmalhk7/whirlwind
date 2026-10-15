package settingsfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go.yaml.in/yaml/v3"
	"io"
	"whirlwind/internal/workflow"
)

func encode(doc Document) ([]byte, error) {
	b, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	var fields map[string]any
	if err = json.Unmarshal(b, &fields); err != nil {
		return nil, err
	}
	b, err = yaml.Marshal(fields)
	return append([]byte("# Whirlwind configuration. UI saves update this file; valid edits apply without restart.\n"), b...), err
}

func decode(raw []byte) (Document, error) {
	doc := Document{Workers: workflow.DefaultSettings()}
	fields, err := decodeYAML(raw)
	if err != nil {
		return doc, err
	}
	b, err := json.Marshal(fields)
	if err != nil {
		return doc, err
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&doc); err != nil {
		return doc, err
	}
	return doc, validate(doc)
}

func decodeYAML(raw []byte) (map[string]any, error) {
	if len(raw) > 1024*1024 {
		return nil, errors.New("settings.yaml exceeds 1 MiB")
	}
	var fields map[string]any
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&fields); err != nil {
		return nil, err
	}
	if err := validateShape(fields); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("settings.yaml must contain one document")
	}
	return fields, nil
}

func validateShape(fields map[string]any) error {
	for _, key := range []string{"version", "workers", "providers", "agents"} {
		if _, ok := fields[key]; !ok {
			return fmt.Errorf("settings.yaml requires %s", key)
		}
	}
	if _, ok := fields["workers"].(map[string]any); !ok {
		return errors.New("workers must be a mapping")
	}
	if _, ok := fields["providers"].([]any); !ok {
		return errors.New("providers must be a list")
	}
	if _, ok := fields["agents"].(map[string]any); !ok {
		return errors.New("agents must be a mapping")
	}
	return nil
}
