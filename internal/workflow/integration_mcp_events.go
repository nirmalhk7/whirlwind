package workflow

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func integrationMCPEventResponse(data []byte, id int64) ([]byte, error) {
	var event bytes.Buffer
	reader := bufio.NewScanner(bytes.NewReader(data))
	reader.Buffer(make([]byte, 1024), integrationMCPResponseLimit)
	for reader.Scan() {
		line := reader.Text()
		if line == "" {
			if response, found := integrationMCPEventData(event.Bytes(), id); found {
				return response, nil
			}
			event.Reset()
			continue
		}
		if value, ok := strings.CutPrefix(line, "data:"); ok {
			if event.Len() > 0 {
				event.WriteByte('\n')
			}
			event.WriteString(strings.TrimSpace(value))
		}
	}
	if reader.Err() != nil {
		return nil, errors.New("selected HTTP MCP event stream exceeded supported size")
	}
	if response, found := integrationMCPEventData(event.Bytes(), id); found {
		return response, nil
	}
	return nil, errors.New("selected HTTP MCP event stream omitted the matching response")
}

func integrationMCPEventData(data []byte, id int64) ([]byte, bool) {
	var envelope struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(data, &envelope) != nil || string(envelope.ID) != fmt.Sprint(id) {
		return nil, false
	}
	return bytes.Clone(data), true
}
