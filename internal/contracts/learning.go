package contracts

// learningSchema describes optional metadata v1; provenance is assigned by the harness.
func learningSchema() map[string]any {
	text := map[string]any{"type": "string"}
	boolean := map[string]any{"type": "boolean"}
	references := array(text)
	lesson := object(map[string]any{
		"trigger": text, "mistake": text, "correction": text,
		"paths": references, "symbols": references, "error_signature": text,
		"evidence_ids": references, "secret": boolean, "isSecret": boolean,
	}, "trigger", "mistake", "correction")
	feedback := object(map[string]any{
		"lesson_id": text, "verdict": map[string]any{"enum": []string{"supports", "contradicts", "supersedes"}},
		"reason": text, "paths": references, "evidence_ids": references,
		"secret": boolean, "isSecret": boolean,
	}, "lesson_id", "verdict", "reason")
	return object(map[string]any{"lessons": array(lesson), "feedback": array(feedback)})
}
