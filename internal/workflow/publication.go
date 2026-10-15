package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (step *taskStep) publicationIntent(p model.Publication) (string, model.Publication, int, error) {
	p.Task = step.task.ID
	p.PlanVersion = step.task.PlanVersion
	p.Repo = step.project.Repo
	p.PolicyHash = step.project.PolicyHash
	b, err := json.Marshal(p)
	if err != nil {
		return "", p, 0, err
	}
	sum := sha256.Sum256(b)
	p.Fingerprint = hex.EncodeToString(sum[:])
	id := fmt.Sprintf("%s:%s:%d", p.Kind, p.Task, p.PlanVersion)
	version := 0
	record, err := step.Store.Get(step.ctx, "publications", id)
	if err == nil {
		old, matchErr := matchPublicationIntent(record, p)
		if matchErr != nil {
			return id, p, 0, matchErr
		}
		if old.Stage == "complete" {
			return id, old, record.Version, nil
		}
		version = record.Version
	} else if !errors.Is(err, store.ErrNotFound) {
		return id, p, 0, err
	}
	p.Owner = step.owner
	p.Stage = "running"
	p.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	err = step.Store.Put(step.ctx, "publications", id, p, version)
	return id, p, version + 1, err
}
func matchPublicationIntent(record model.Record, requested model.Publication) (model.Publication, error) {
	previous, err := store.Decode[model.Publication](record)
	if err != nil {
		return previous, err
	}
	if previous.Fingerprint != requested.Fingerprint {
		return previous, errors.New("publication request changed; inspect earlier remote intent before retry")
	}
	return previous, nil
}

func (step *taskStep) publicationFailure(id string, p model.Publication, version int, remoteErr error) error {
	p.Stage = "uncertain"
	p.Error = remoteErr.Error()
	return errors.Join(remoteErr, step.Store.Put(step.ctx, "publications", id, p, version))
}
