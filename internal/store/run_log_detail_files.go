package store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/runlog"
)

func (logs *runLogStorage) ensureFile(ctx context.Context, s *Store, id, path string) error {
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return s.recordRunLogChange(ctx, "sessions", id)
	}
	if err != nil {
		return err
	}
	return logs.ensureDetails(ctx, s, id, path)
}

func (logs *runLogStorage) ensureDetails(ctx context.Context, s *Store, id, path string) error {
	_, err := os.Stat(path + ".details")
	if err == nil {
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	body, err := s.runLogDetails(ctx, id)
	if err != nil || len(body) == 0 {
		return err
	}
	return logs.sync(ctx, s, id, body)
}

func (state *persistedRunLog) append(ctx context.Context, writer io.Writer, details []byte) error {
	if err := state.feed.append(ctx, writer); err != nil {
		return err
	}
	if replay, ok := writer.(*runLogReplayWriter); ok {
		if err := replay.finish(); err != nil {
			return err
		}
	}
	if !state.detailsChecked && details == nil {
		var err error
		details, err = state.feed.database.runLogDetails(ctx, state.feed.id)
		if err != nil {
			return err
		}
	}
	state.detailsChecked = true
	return state.appendDetails(writer, details)
}

func (state *persistedRunLog) appendDetails(writer io.Writer, body []byte) error {
	if len(body) == 0 || runLogDetailsHash(body) == state.detailsHash {
		return nil
	}
	event := model.AttemptEvent{AttemptID: state.feed.id, Kind: "run/details", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	var detailsWriter runlog.Writer
	if err := detailsWriter.Write(writer, runlog.Record{Event: event, Body: body}); err != nil {
		return err
	}
	state.detailsHash, state.detailsDirty = runLogDetailsHash(body), true
	return nil
}

func runLogDetailsHash(body []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(body))
}
