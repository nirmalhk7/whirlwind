package runlog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"whirlwind/internal/model"
)

func writeEntry(writer io.Writer, event model.AttemptEvent, body []byte, truncated bool) error {
	prefix := fmt.Sprintf("%s run=%s", event.CreatedAt, event.AttemptID)
	if event.Sequence > 0 {
		prefix += fmt.Sprintf(" seq=%d", event.Sequence)
	}
	prefix += " stype=" + event.Kind
	if truncated {
		prefix += " truncated=true"
	}
	_, err := fmt.Fprintf(writer, "%s %s\n", prefix, runLogPayload(event.Kind, body))
	return err
}

func runLogPayload(kind string, body []byte) string {
	switch kind {
	case "prompt", "final_output":
		return "message=" + quoteRunLogText(string(body))
	case "session/update":
		if message := runLogSessionMessage(body); message != "" {
			return message
		}
	}
	var compact bytes.Buffer
	if json.Compact(&compact, body) == nil {
		return "data=" + compact.String()
	}
	return "message=" + quoteRunLogText(string(body))
}

func quoteRunLogText(text string) string {
	var quoted bytes.Buffer
	encoder := json.NewEncoder(&quoted)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(text)
	return strings.TrimSuffix(quoted.String(), "\n")
}

type runLogUpdate struct {
	Kind      string `json:"sessionUpdate"`
	MessageID string `json:"messageId"`
	Content   struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

type runLogNotification struct {
	Params struct {
		Update runLogUpdate `json:"update"`
	} `json:"params"`
}

func decodeRunLogMessage(body []byte) runLogUpdate {
	var notification runLogNotification
	if json.Unmarshal(body, &notification) != nil {
		return runLogUpdate{}
	}
	update := notification.Params.Update
	if update.Content.Type != "text" || (update.Kind != "agent_message_chunk" && update.Kind != "user_message_chunk") {
		return runLogUpdate{}
	}
	return update
}

func runLogSessionMessage(body []byte) string {
	update := decodeRunLogMessage(body)
	if update.Kind == "" {
		return ""
	}
	prefix := "update=" + update.Kind
	if update.MessageID != "" {
		prefix += " m_id=" + quoteRunLogText(update.MessageID)
	}
	return prefix + " message=" + quoteRunLogText(update.Content.Text)
}
