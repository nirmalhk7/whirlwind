package workflow

import (
	"encoding/json"
	"whirlwind/internal/contracts"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

// decodeLearning recognizes only an explicit envelope; old provider outputs stay valid.
func decodeLearning(text string) (string, model.LearningReport, []string) {
	var envelope map[string]json.RawMessage
	if provider.JSON(text, &envelope) != nil {
		return text, model.LearningReport{}, nil
	}
	result, hasResult := envelope["result"]
	raw, hasLearning := envelope["learning"]
	if !hasResult || !hasLearning {
		return text, model.LearningReport{}, nil
	}
	var value string
	if json.Unmarshal(result, &value) != nil {
		value = string(result)
	}
	var report model.LearningReport
	if len(raw) > 8192 || json.Unmarshal(raw, &report) != nil || contracts.ValidateJSON("learning_v1", raw) != nil {
		return value, model.LearningReport{}, []string{"Malformed learning metadata ignored"}
	}
	return value, report, nil
}
