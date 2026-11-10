package provider

import "testing"

// Combining earlier progress with final JSON makes structured role output invalid.
func TestACPFinalMessageExcludesEarlierProgress(t *testing.T) {
	session := providerSession{}
	emit := func(id, text string) {
		session.sessionUpdate(map[string]any{"update": map[string]any{"sessionUpdate": "agent_message_chunk", "messageId": id, "content": map[string]any{"type": "text", "text": text}}})
	}
	emit("progress", "Reading supplied input.\n")
	emit("final", "[")
	emit("final", "1]")
	if session.output.Text != "[1]" {
		t.Fatalf("final output contaminated by earlier message: %q", session.output.Text)
	}
}
