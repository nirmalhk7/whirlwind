package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/settingsfile"
	"whirlwind/internal/store"
)

// prepareMutation persists intent before external onboarding or evaluation begins.
func (s *Server) prepareMutation(w http.ResponseWriter, r *http.Request, m mutation) (*store.Change, error) {
	parsed, parseErr := parseRequest(s, w, r, nil)
	external := parseErr == nil && r.Method == "POST" && parsed.kind == "projects" && (parsed.id == "" || parsed.action == "embedding-evaluation")
	if !external {
		return nil, nil
	}
	return s.beginMutationJournal(r, m)
}

func (s *Server) mutationJournalVersion(ctx context.Context, id, fingerprint string) (int, error) {
	old, err := s.Store.Get(ctx, "mutations", id)
	if errors.Is(err, store.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var value map[string]any
	_ = json.Unmarshal(old.Data, &value)
	if value["fingerprint"] != fingerprint {
		return 0, store.ErrConflict
	}
	if value["stage"] == "cancelled" {
		return 0, store.ErrConflict
	}
	return old.Version, nil
}

func (s *Server) beginMutationJournal(r *http.Request, m mutation) (*store.Change, error) {
	sum := sha256.Sum256([]byte(m.key))
	id := hex.EncodeToString(sum[:])
	version, err := s.mutationJournalVersion(r.Context(), id, m.fingerprint)
	if err != nil {
		return nil, err
	}
	body, err := store.SafeJSON(json.RawMessage(m.body))
	if err != nil {
		return nil, err
	}
	value := map[string]any{"kind": r.Method + " " + r.URL.Path, "stage": "running", "owner": s.Engine.Owner(), "fingerprint": m.fingerprint, "input": json.RawMessage(body), "started_at": time.Now().UTC().Format(time.RFC3339Nano)}
	if err = s.Store.Put(r.Context(), "mutations", id, value, version); err != nil {
		return nil, err
	}
	return &store.Change{Kind: "mutations", ID: id, Value: value, Expected: version + 1}, nil
}

func mutationReceipt(m mutation, buffer *responseBuffer) *store.Receipt {
	if buffer.status >= 500 {
		return nil
	}
	return &store.Receipt{Key: m.key, Fingerprint: m.fingerprint, Status: buffer.status, Body: buffer.body}
}

func finishMutationJournal(journal *store.Change, status int) []store.Change {
	extras := []store.Change{}
	if journal == nil {
		return extras
	}
	value := journal.Value.(map[string]any)
	value["stage"] = "complete"
	if status >= 400 {
		value["stage"] = "failed"
	}
	value["finished_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	return append(extras, *journal)
}

// commitMutation persists the buffered response only after route execution finishes.
func (s *Server) commitMutation(r *http.Request, batch *store.Batch, buffer *responseBuffer, m mutation, journal *store.Change) error {
	receipt := mutationReceipt(m, buffer)
	extras := finishMutationJournal(journal, buffer.status)
	var err error
	if buffer.status < 400 {
		extras = append(extras, store.Change{Kind: "audit", ID: store.ID(), Value: model.Audit{Source: "local_operator", Action: r.Method + " " + r.URL.Path, Target: r.URL.Path, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}})
		if r.Method == "PATCH" && r.URL.Path == "/api/v1/settings/file" {
			var input settingsFileInput
			if err = json.Unmarshal(m.body, &input); err != nil {
				return err
			}
			source, ok := s.Store.Source.(*settingsfile.Source)
			if !ok {
				return &apiError{503, "settings_file_unavailable", "No startup settings file is attached to this process"}
			}
			err = source.SaveFile(r.Context(), input.Content, input.Revision, func(changes []store.Change) error {
				return batch.Commit(store.WithoutSettingsSource(r.Context()), s.Store, receipt, append(extras, changes...)...)
			})
		} else {
			err = batch.Commit(r.Context(), s.Store, receipt, extras...)
		}
		if errors.Is(err, store.ErrProjectBusy) {
			return &apiError{409, "project_busy", err.Error()}
		}
	} else {
		err = s.Store.Write(r.Context(), extras, receipt)
	}
	return mutationPersistenceError(err)
}

func mutationPersistenceError(err error) error {
	if err == nil || errors.Is(err, store.ErrConflict) {
		return err
	}
	return &apiError{503, "persistence_failed", err.Error()}
}
