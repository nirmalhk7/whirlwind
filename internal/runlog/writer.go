package runlog

import (
	"encoding/json"
	"io"
	"strings"

	"whirlwind/internal/model"
)

type Writer struct {
	pending *runLogPendingMessage
}

type Record struct {
	Event     model.AttemptEvent
	Body      []byte
	Truncated bool
}

type runLogPendingMessage struct {
	first  Record
	update runLogUpdate
	text   strings.Builder
	after  []Record
}

func (feed *Writer) Write(writer io.Writer, record Record) error {
	if record.Event.Kind == "session/update" {
		update := decodeRunLogMessage(record.Body)
		if update.Kind != "" && update.MessageID != "" {
			return feed.messageChunk(writer, record, update)
		}
	}
	if feed.pending != nil && !runLogMessageBoundary(record.Event.Kind) {
		feed.pending.after = append(feed.pending.after, record)
		return nil
	}
	if err := feed.Flush(writer); err != nil {
		return err
	}
	return writeEntry(writer, record.Event, record.Body, record.Truncated)
}

func runLogMessageBoundary(kind string) bool {
	switch kind {
	case "whirlwind/stop", "whirlwind/process_exit", "final_output", "prompt":
		return true
	}
	return false
}

func (feed *Writer) messageChunk(writer io.Writer, record Record, update runLogUpdate) error {
	if pending := feed.pending; pending != nil && (pending.update.MessageID != update.MessageID || pending.update.Kind != update.Kind) {
		if err := feed.Flush(writer); err != nil {
			return err
		}
	}
	if feed.pending == nil {
		feed.pending = &runLogPendingMessage{first: record, update: update}
	}
	feed.pending.text.WriteString(update.Content.Text)
	feed.pending.first.Truncated = feed.pending.first.Truncated || record.Truncated
	return nil
}

func (feed *Writer) Flush(writer io.Writer) error {
	pending := feed.pending
	if pending == nil {
		return nil
	}
	var notification runLogNotification
	notification.Params.Update = pending.update
	notification.Params.Update.Content.Text = pending.text.String()
	body, _ := json.Marshal(notification)
	if err := writeEntry(writer, pending.first.Event, body, pending.first.Truncated); err != nil {
		return err
	}
	for _, record := range pending.after {
		if err := writeEntry(writer, record.Event, record.Body, record.Truncated); err != nil {
			return err
		}
	}
	feed.pending = nil
	return nil
}
