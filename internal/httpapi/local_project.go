package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/projectregistry"
	"whirlwind/internal/store"
)

func (s *Server) registerLocalProject(ctx context.Context, command LocalCommand) (json.RawMessage, error) {
	if command.IdempotencyKey == "" {
		return nil, errors.New("project registration idempotency key required")
	}
	payload := localPayload(command.Payload)
	fingerprint := localFingerprint("project.register", command, payload)
	status, cached, err := s.Store.Idempotent(ctx, command.IdempotencyKey, fingerprint)
	if err != nil {
		return nil, err
	}
	if status != 0 {
		return cached, nil
	}
	project, err := createLocalProject(ctx, payload)
	if err != nil {
		return nil, err
	}
	return s.saveLocalProject(ctx, command, fingerprint, project)
}

func createLocalProject(ctx context.Context, payload json.RawMessage) (model.Project, error) {
	var input projectInput
	if err := json.Unmarshal(payload, &input); err != nil {
		return model.Project{}, err
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || !filepath.IsAbs(input.Path) {
		return model.Project{}, errors.New("project name and absolute local Git repository path required")
	}
	if input.WebhookURL != "" {
		return model.Project{}, errors.New("webhook setup requires an explicitly selected authorized integration")
	}
	return localProject(ctx, input)
}

func (s *Server) saveLocalProject(ctx context.Context, command LocalCommand, fingerprint string, project model.Project) (json.RawMessage, error) {
	data, err := json.Marshal(project)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(model.Record{ID: project.ID, Version: 1, Data: data})
	if err != nil {
		return nil, err
	}
	receipt := &store.Receipt{Key: command.IdempotencyKey, Fingerprint: fingerprint, Status: 201, Body: body}
	audit := model.Audit{Source: "local_operator", Action: "project.register", Target: project.ID, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := s.Store.Write(ctx, []store.Change{{Kind: "projects", ID: project.ID, Value: project}, {Kind: "audit", ID: store.ID(), Value: audit}}, receipt); err != nil {
		return nil, err
	}
	return body, nil
}

func localProject(ctx context.Context, input projectInput) (model.Project, error) {
	return projectregistry.Load(ctx, input.Name, input.Path)
}

func localFingerprint(operation string, command LocalCommand, payload []byte) string {
	value := fmt.Sprintf("%s %s %d %s %s", operation, command.ResourceID, command.ExpectedVersion, command.ContentHash, payload)
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
