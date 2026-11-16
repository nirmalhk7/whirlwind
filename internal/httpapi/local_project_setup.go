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
	projectRecord, err := s.Store.Get(ctx, "projects", command.ProjectID)
	if err != nil {
		return nil, err
	}
	if projectRecord.Version != command.ExpectedVersion {
		return nil, store.ErrConflict
	}
	project, err := store.Decode[model.Project](projectRecord)
	if err != nil {
		return nil, err
	}
	if project.Paused || project.Status != "error" && project.Status != "approved" {
		return nil, store.ErrConflict
	}
	operations, err := s.Store.List(ctx, "operations")
	if err != nil {
		return nil, err
	}
	latest, latestOrder := model.Operation{}, int64(-1)
	for _, record := range operations {
		operation, decodeErr := store.Decode[model.Operation](record)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if operation.Kind == "project_setup" && operation.Subject == project.ID && record.CreatedOrder > latestOrder {
			latest, latestOrder = operation, record.CreatedOrder
		}
	}
	failedRetry := project.Status == "error" && latest.Stage == "failed"
	completedRecheck := project.Status == "approved" && latest.Stage == "complete"
	if latestOrder < 0 || !failedRetry && !completedRecheck {
		return nil, store.ErrConflict
	}
	for _, record := range operations {
		operation, decodeErr := store.Decode[model.Operation](record)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if operation.Kind == "project_setup" && operation.Subject == project.ID && (operation.Stage == "queued" || operation.Stage == "running") {
			return nil, store.ErrConflict
		}
	}
	id := store.ID()
	operation := model.Operation{Kind: "project_setup", Subject: project.ID, Stage: "queued", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if completedRecheck {
		operation.Input = map[string]bool{"onboarding": true}
	} else {
		previousInput, _ := json.Marshal(latest.Input)
		var previousEvidence struct {
			Onboarding bool `json:"onboarding"`
		}
		_ = json.Unmarshal(previousInput, &previousEvidence)
		if previousEvidence.Onboarding {
			operation.Input = map[string]bool{"onboarding": true}
		}
	}
	previousInput, _ := json.Marshal(latest.Input)
	var previousEvidence struct {
		Onboarding bool `json:"onboarding"`
	}
	_ = json.Unmarshal(previousInput, &previousEvidence)
	if previousEvidence.Onboarding {
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
	receipt := &store.Receipt{Key: command.IdempotencyKey, Fingerprint: fingerprint, Status: 202, Body: body}
	changes := []store.Change{
		{Kind: "projects", ID: project.ID, Value: project, Expected: projectRecord.Version},
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
