package dashboard

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/rivo/tview"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (d *Dashboard) showRunLogs(index int) {
	if index < 0 || index >= len(d.runRows) {
		return
	}
	d.selectRun(index, "", "", 0)
	row := d.runRows[index]
	view := tview.NewTextView().SetScrollable(true).SetText(d.detail.GetText(false) + "\nLoading saved run logs…")
	view.SetBorder(true).SetTitle(" Run logs · " + row.id + " · Esc to close ")
	d.modal = view
	d.SetRoot(view, true).SetFocus(view)
	header := d.detail.GetText(false)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		logs, err := savedRunLogs(ctx, filepath.Join(d.dataDirectory, "whirlwind.db"), row.id)
		d.QueueUpdateDraw(func() {
			if d.modal != view {
				return
			}
			if err != nil {
				view.SetText(header + "\nCannot read saved run logs: " + err.Error())
				return
			}
			view.SetText(header + "\n" + logs)
		})
	}()
}

func savedRunLogs(ctx context.Context, path, id string) (string, error) {
	database, err := store.OpenReadOnly(path)
	if err != nil {
		return "", err
	}
	defer database.Close()
	record, err := database.Get(ctx, "sessions", id)
	if err != nil {
		return "", err
	}
	attempt, err := store.Decode[model.Attempt](record)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	var cursor int64
	seen := map[string]bool{}
	for {
		events, err := database.AttemptEvents(ctx, id, cursor, 100)
		if err != nil {
			return "", err
		}
		if len(events) == 0 {
			break
		}
		for _, event := range events {
			fmt.Fprintf(&text, "\n--- %d · %s · %s ---\n", event.Sequence, event.Kind, event.CreatedAt)
			if event.Artifact != nil {
				if err := appendRunArtifact(ctx, database, id, event.Artifact.ID, &text); err != nil {
					return "", err
				}
				seen[event.Artifact.ID] = true
			} else {
				text.Write(event.Metadata)
				text.WriteByte('\n')
			}
			cursor = event.Sequence
		}
	}
	if attempt.OutputArtifact != "" && !seen[attempt.OutputArtifact] {
		if err := appendRunArtifact(ctx, database, id, attempt.OutputArtifact, &text); err != nil {
			return "", err
		}
	}
	if text.Len() == 0 {
		text.WriteString("No saved log events or output artifacts for this run.\n")
	}
	return text.String(), nil
}

func appendRunArtifact(ctx context.Context, database *store.Store, runID, artifactID string, text *strings.Builder) error {
	metadata, body, err := database.Artifact(ctx, artifactID)
	if err != nil {
		return err
	}
	if metadata.OwnerKind != "sessions" || metadata.AttemptID != runID {
		return fmt.Errorf("artifact does not belong to selected run")
	}
	text.Write(body)
	text.WriteByte('\n')
	if metadata.Truncated {
		text.WriteString("[Artifact truncated when originally saved; remaining content unavailable.]\n")
	}
	return nil
}
