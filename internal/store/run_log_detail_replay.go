package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

func persistedRunLogDetails(line []byte) ([]byte, bool) {
	prefix, fields, ok := bytes.Cut(line, []byte(" stype="))
	if !ok || bytes.Contains(prefix, []byte(" seq=")) {
		return nil, false
	}
	body, ok := bytes.CutPrefix(fields, []byte("run/details data="))
	return bytes.TrimSuffix(body, []byte("\n")), ok
}

func (writer *runLogReplayWriter) nextLine() ([]byte, error) {
	for {
		line, err := writer.reader.ReadBytes('\n')
		start := writer.offset
		writer.offset += int64(len(line))
		body, details := persistedRunLogDetails(line)
		if details && err == io.EOF {
			return nil, writer.repairDetailsTail(start)
		}
		if !details || !json.Valid(body) {
			return line, err
		}
		writer.state.detailsHash = runLogDetailsHash(body)
		writer.state.detailsDirty = true
	}
}

func (writer *runLogReplayWriter) repairDetailsTail(offset int64) error {
	if err := writer.file.Truncate(offset); err != nil {
		return err
	}
	if _, err := writer.file.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	writer.reader = nil
	return io.EOF
}

func (writer *runLogReplayWriter) finish() error {
	if writer.reader == nil {
		return nil
	}
	line, err := writer.nextLine()
	if len(line) > 0 {
		return errors.New("run log contains entries absent from saved events")
	}
	if err == io.EOF {
		writer.reader = nil
		return nil
	}
	return err
}
