package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
	"whirlwind/internal/model"
)

func eventCursor(r *http.Request) (int64, error) {
	raw := r.URL.Query().Get("after")
	if raw == "" {
		raw = r.Header.Get("Last-Event-ID")
	}
	if raw == "" {
		raw = "0"
	}
	cursor, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || cursor < 0 || cursor > 9007199254740991 {
		return 0, &apiError{422, "invalid_cursor", "Non-negative safe integer cursor required"}
	}
	return cursor, nil
}

// events validates a replay cursor and streams updates until disconnection.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	cursor, err := eventCursor(r)
	if err != nil {
		s.error(w, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.error(w, &apiError{500, "stream_unavailable", "Streaming unsupported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	s.streamEvents(w, r, flusher, cursor)
}
func (s *Server) streamEvents(w http.ResponseWriter, r *http.Request, flusher http.Flusher, cursor int64) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		events, err := s.Store.Events(r.Context(), cursor, 100)
		if err != nil {
			return
		}
		next, err := writeEvents(w, events, cursor)
		if err != nil {
			return
		}
		cursor = next
		flusher.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}
func writeEvents(w io.Writer, events []model.Event, cursor int64) (int64, error) {
	for _, event := range events {
		b, _ := json.Marshal(event)
		if _, err := fmt.Fprintf(w, "id: %d\nevent: update\ndata: %s\n\n", event.ID, b); err != nil {
			return cursor, err
		}
		cursor = event.ID
	}
	var err error
	if len(events) == 0 {
		_, err = io.WriteString(w, ": heartbeat\n\n")
	}
	return cursor, err
}
