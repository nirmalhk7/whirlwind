// Package contracts validates versioned role outputs without remote schema loading.
package contracts

import (
	"encoding/json"
	"fmt"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"sync"
)

var schemas = sync.OnceValues(compile)

func Validate(name string, value any) error {
	all, err := schemas()
	if err != nil {
		return err
	}
	schema, ok := all[name]
	if !ok {
		return fmt.Errorf("unknown output contract %q", name)
	}
	return schema.Validate(value)
}

func ValidateJSON(name string, raw []byte) error {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	return Validate(name, value)
}

func compile() (map[string]*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	out := map[string]*jsonschema.Schema{}
	for name, definition := range definitions() {
		address := "https://whirlwind.invalid/contracts/v1/" + name
		if err := compiler.AddResource(address, jsonValue(definition)); err != nil {
			return nil, err
		}
		schema, err := compiler.Compile(address)
		if err != nil {
			return nil, err
		}
		out[name] = schema
	}
	return out, nil
}

func object(properties map[string]any, required ...string) map[string]any {
	definition := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		definition["required"] = required
	}
	return definition
}
func array(item any) map[string]any {
	return map[string]any{"type": []string{"array", "null"}, "items": item}
}

func definitions() map[string]any {
	text := map[string]any{"type": "string", "minLength": 1, "pattern": `\S`}
	boolean := map[string]any{"type": "boolean"}
	strings := array(text)
	criteria := array(text)
	criteria["type"] = "array"
	criteria["minItems"] = 1
	question := object(map[string]any{"id": text, "text": text, "answer": map[string]any{"type": "string"}, "secret": boolean, "options": strings}, "id", "text")
	unit := object(map[string]any{"id": text, "objective": text, "dependencies": strings, "allowed_paths": strings}, "id", "objective")
	direction := object(map[string]any{"category": map[string]any{"enum": []string{"expand", "optimize", "harden", "simplify"}}, "title": text, "description": text}, "category", "title", "description")
	directions := array(direction)
	directions["type"] = "array"
	directions["minItems"] = 4
	directions["maxItems"] = 4
	finding := object(map[string]any{"message": text, "file": map[string]any{"type": "string"}, "line": map[string]any{"type": "integer", "minimum": 0}, "blocking": boolean, "resolved": boolean}, "message")
	return map[string]any{
		"learning_v1": learningSchema(),
		"directions":  directions,
		"plan":        object(map[string]any{"objective": text, "acceptance_criteria": criteria, "units": array(unit), "questions": array(question)}, "objective", "acceptance_criteria"),
		"review":      object(map[string]any{"score": map[string]any{"type": "number", "minimum": 0, "maximum": 10}, "findings": array(finding), "summary": map[string]any{"type": "string"}}, "score", "findings"),
		"novelty":     object(map[string]any{"unique": boolean, "reason": map[string]any{"type": "string"}}, "unique"),
	}
}

func jsonValue(value any) any {
	b, _ := json.Marshal(value)
	var result any
	_ = json.Unmarshal(b, &result)
	return result
}
