package store

import (
	"context"
	"fmt"
	"io"

	"whirlwind/internal/model"
	"whirlwind/internal/runlog"
)

type runLogFeed struct {
	database *Store
	id       string
	cursor   int64
	seen     map[string]bool
	writer   runlog.Writer
	finished bool
}

// Only append events after the durable cursor, including every inspection page.
func (feed *runLogFeed) append(ctx context.Context, writer io.Writer) error {
	if err := feed.drainEvents(ctx, writer); err != nil {
		return err
	}
	record, err := feed.database.Get(ctx, "sessions", feed.id)
	if err != nil {
		return err
	}
	attempt, err := Decode[model.Attempt](record)
	if err != nil {
		return err
	}
	return feed.finish(ctx, writer, attempt)
}

func (feed *runLogFeed) drainEvents(ctx context.Context, writer io.Writer) error {
	for {
		events, err := feed.database.AttemptEvents(ctx, feed.id, feed.cursor, 100)
		if err != nil {
			return err
		}
		if len(events) == 0 {
			break
		}
		for _, event := range events {
			if err := feed.event(ctx, writer, event); err != nil {
				return err
			}
			feed.cursor = event.Sequence
		}
	}
	return nil
}

func (feed *runLogFeed) event(ctx context.Context, writer io.Writer, event model.AttemptEvent) error {
	if event.Artifact == nil {
		return feed.writer.Write(writer, runlog.Record{Event: event, Body: event.Metadata})
	}
	id := event.Artifact.ID
	if feed.seen[id] {
		return nil
	}
	metadata, body, err := feed.artifact(ctx, id)
	if err != nil {
		return err
	}
	if event.Kind == "" {
		event.AttemptID, event.Kind, event.CreatedAt = metadata.AttemptID, metadata.Kind, metadata.CreatedAt
	}
	if err := feed.writer.Write(writer, runlog.Record{Event: event, Body: body, Truncated: metadata.Truncated}); err != nil {
		return err
	}
	feed.seen[id] = true
	return nil
}

func (feed *runLogFeed) artifact(ctx context.Context, id string) (model.Artifact, []byte, error) {
	metadata, body, err := feed.database.Artifact(ctx, id)
	if err != nil {
		return metadata, nil, err
	}
	if metadata.OwnerKind != "sessions" || metadata.AttemptID != feed.id {
		return metadata, nil, fmt.Errorf("artifact does not belong to selected run")
	}
	return metadata, body, nil
}

func (feed *runLogFeed) finish(ctx context.Context, writer io.Writer, attempt model.Attempt) error {
	feed.finished = attempt.Finished
	if attempt.Finished {
		if err := feed.drainEvents(ctx, writer); err != nil {
			return err
		}
		if err := feed.writer.Flush(writer); err != nil {
			return err
		}
	}
	if attempt.OutputArtifact != "" {
		return feed.event(ctx, writer, model.AttemptEvent{Artifact: &model.Artifact{ID: attempt.OutputArtifact}})
	}
	return nil
}
