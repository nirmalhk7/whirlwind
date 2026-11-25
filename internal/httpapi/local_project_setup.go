package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (s *Server) retryLocalProjectSetup(ctx context.Context, command LocalCommand) (json.RawMessage, error) {
	if command.ProjectID == "" || command.IdempotencyKey == "" || command.ExpectedVersion < 1 {
		return nil, errors.New("project setup retry requires project ID, expected version, and idempotency key")
	}
	fingerprint := projectSetupRetryFingerprint(command)
	status, cached, err := s.Store.Idempotent(ctx, command.IdempotencyKey, fingerprint)
	if err != nil {
		return nil, err
	}
	if status != 0 {
		return cached, nil
	}
	project, latest, err := s.projectSetupRetryTarget(ctx, command)
	if err != nil {
		return nil, err
	}
	return s.queueProjectSetupRetry(ctx, project, latest, command.ExpectedVersion, command.IdempotencyKey, fingerprint)
}

func (s *Server) projectSetupRetryTarget(ctx context.Context, command LocalCommand) (model.Project, model.Operation, error) {
	record, err := s.Store.Get(ctx, "projects", command.ProjectID)
	if err != nil {
		return model.Project{}, model.Operation{}, err
	}
	if record.Version != command.ExpectedVersion {
		return model.Project{}, model.Operation{}, store.ErrConflict
	}
	project, err := store.Decode[model.Project](record)
	if err != nil {
		return model.Project{}, model.Operation{}, err
	}
	if project.Paused || project.Status != "error" && project.Status != "approved" {
		return model.Project{}, model.Operation{}, store.ErrConflict
	}
	latest, err := s.retryableProjectSetup(ctx, project)
	return project, latest, err
}

func (s *Server) retryableProjectSetup(ctx context.Context, project model.Project) (model.Operation, error) {
	records, err := s.Store.List(ctx, "operations")
	if err != nil {
		return model.Operation{}, err
	}
	var latest model.Operation
	latestOrder := int64(-1)
	active := false
	for _, record := range records {
		operation, err := store.Decode[model.Operation](record)
		if err != nil {
			return model.Operation{}, err
		}
		if operation.Kind != "project_setup" || operation.Subject != project.ID {
			continue
		}
		if record.CreatedOrder > latestOrder {
			latest, latestOrder = operation, record.CreatedOrder
		}
		active = active || operation.Stage == "queued" || operation.Stage == "running"
	}
	if active || latestOrder < 0 || !projectSetupRetryAllowed(project.Status, latest.Stage) {
		return model.Operation{}, store.ErrConflict
	}
	return latest, nil
}

func projectSetupRetryAllowed(status, stage string) bool {
	return status == "error" && stage == "failed" || status == "approved" && stage == "complete"
}

func setupRetryIncludesOnboarding(project model.Project, previous model.Operation) bool {
	if project.Status == "approved" && previous.Stage == "complete" {
		return true
	}
	var evidence struct {
		Onboarding bool `json:"onboarding"`
	}
	_ = json.Unmarshal(encodedInput(previous.Input), &evidence)
	return evidence.Onboarding
}

func (s *Server) queueProjectSetupRetry(ctx context.Context, project model.Project, previous model.Operation, version int, key, fingerprint string) (json.RawMessage, error) {
	id := store.ID()
	operation := model.Operation{Kind: "project_setup", Subject: project.ID, Stage: "queued", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if setupRetryIncludesOnboarding(project, previous) {
		operation.Input = map[string]bool{"onboarding": true}
	}
	data, err := json.Marshal(operation)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(model.Record{ID: id, Version: 1, Data: data})
	if err != nil {
		return nil, err
	}
	project.Status, project.QualityIdentity = "draft", nil
	audit := model.Audit{Source: "local_operator", Action: "project.setup.retry", Target: project.ID, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	receipt := &store.Receipt{Key: key, Fingerprint: fingerprint, Status: 202, Body: body}
	changes := []store.Change{
		{Kind: "projects", ID: project.ID, Value: project, Expected: version},
		{Kind: "operations", ID: id, Value: operation},
		{Kind: "audit", ID: store.ID(), Value: audit},
	}
	if err := s.Store.Write(ctx, changes, receipt); err != nil {
		return nil, err
	}
	store.AfterCommit(ctx, func() {
		if s.Engine != nil {
			s.Engine.Wake()
		}
	})
	return body, nil
}

func encodedInput(value any) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}

func projectSetupRetryFingerprint(command LocalCommand) string {
	value := fmt.Sprintf("%q %q %d", "project.setup.retry", command.ProjectID, command.ExpectedVersion)
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
