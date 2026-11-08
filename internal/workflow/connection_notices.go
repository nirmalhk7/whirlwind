package workflow

import (
	"context"
	"errors"
	"strings"
	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func connectionFailureMessage(project model.Project, err error) string {
	if github.IsTransient(err) {
		return project.Name + ": GitHub connection interrupted. Retrying automatically."
	}
	return project.Name + ": " + err.Error()
}

func (e *Engine) resolveProjectNotices(ctx context.Context, project model.Project, kind string) error {
	records, err := e.Store.List(ctx, "inbox")
	if err != nil {
		return err
	}
	for _, record := range records {
		notice, err := store.Decode[model.Notification](record)
		if err != nil {
			return err
		}
		if !recoveredProjectNotice(notice, project, kind) {
			continue
		}
		notice.Resolved = true
		if err := e.Store.Put(ctx, "inbox", record.ID, notice, record.Version); err != nil && !errors.Is(err, store.ErrConflict) {
			return err
		}
	}
	return nil
}

func recoveredProjectNotice(notice model.Notification, project model.Project, kind string) bool {
	if notice.Resolved || notice.Kind != kind {
		return false
	}
	if strings.HasPrefix(notice.Message, project.Name+": ") {
		return true
	}
	// Older GitHub transport alerts had no project identity. A successful API read
	// establishes recovery of that shared connection, not of authentication errors.
	return kind == "github" && (strings.HasPrefix(notice.Message, "gh: ") && github.IsTransient(errors.New(notice.Message)))
}
