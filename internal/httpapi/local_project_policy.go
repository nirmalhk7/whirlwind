package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"

	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/store"
)

type localPolicyPreview struct {
	ProjectID string `json:"project_id"`
	Path      string `json:"path"`
	Version   int    `json:"version"`
	Hash      string `json:"hash"`
}

func (s *Server) executeLocalProjectPolicy(ctx context.Context, command LocalCommand) (json.RawMessage, error) {
	if command.Operation == "project.policy.preview" {
		preview, _, _, err := s.readPolicyApproval(ctx, command)
		if err != nil {
			return nil, err
		}
		return json.Marshal(preview)
	}
	return s.commitLocalCommand(ctx, command, func(ctx context.Context) (any, error) {
		return s.approveLocalProjectPolicy(ctx, command)
	})
}

func (s *Server) approveLocalProjectPolicy(ctx context.Context, command LocalCommand) (localPolicyPreview, error) {
	preview, project, document, err := s.readPolicyApproval(ctx, command)
	if err != nil {
		return localPolicyPreview{}, err
	}
	if command.ExpectedVersion != preview.Version || command.ContentHash != preview.Hash {
		return localPolicyPreview{}, store.ErrConflict
	}
	version, err := s.managedPolicyVersion(ctx, project.ID)
	if err != nil {
		return localPolicyPreview{}, err
	}
	if err := s.Store.Put(ctx, policy.ManagedPolicyResource, project.ID, document, version); err != nil {
		return localPolicyPreview{}, err
	}
	project.ManagedPolicy = true
	if err := s.Engine.ConfirmProjectPolicy(ctx, project, preview.Version, project.Policy, preview.Hash); err != nil {
		return localPolicyPreview{}, err
	}
	audit := model.Audit{Source: "local_operator", Action: command.Operation, Target: project.ID, Version: preview.Version, Reason: preview.Hash}
	if err := s.Store.Audit(ctx, audit); err != nil {
		return localPolicyPreview{}, err
	}
	preview.Version++
	return preview, nil
}

func (s *Server) readPolicyApproval(ctx context.Context, command LocalCommand) (localPolicyPreview, model.Project, model.ProjectPolicyDocument, error) {
	var preview localPolicyPreview
	var document model.ProjectPolicyDocument
	project, version, err := s.projectForPolicyApproval(ctx, command.ProjectID)
	if err != nil {
		return preview, project, document, err
	}
	if err := policy.CheckManagedPolicyLocation(s.Engine.Root, project.Path); err != nil {
		return preview, project, document, err
	}
	var input struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(command.Payload, &input); err != nil {
		return preview, project, document, err
	}
	rules, hash, err := policy.ParseManagedPolicy(project.ID, input.Content)
	project.Policy = rules
	return localPolicyPreview{project.ID, project.Path, version, hash}, project, model.ProjectPolicyDocument{ProjectID: project.ID, Content: input.Content}, err
}

func (s *Server) managedPolicyVersion(ctx context.Context, projectID string) (int, error) {
	record, err := s.Store.Get(ctx, policy.ManagedPolicyResource, projectID)
	if errors.Is(err, store.ErrNotFound) {
		return 0, nil
	}
	return record.Version, err
}

func (s *Server) projectForPolicyApproval(ctx context.Context, ref string) (model.Project, int, error) {
	if ref == "" {
		return model.Project{}, 0, errors.New("registered project ID or absolute checkout path required")
	}
	if !filepath.IsAbs(ref) {
		return s.projectForPolicyID(ctx, ref)
	}
	return s.projectForPolicyCheckout(ctx, ref)
}

func (s *Server) projectForPolicyID(ctx context.Context, id string) (model.Project, int, error) {
	record, err := s.Store.Get(ctx, "projects", id)
	if err != nil {
		return model.Project{}, 0, err
	}
	project, err := store.Decode[model.Project](record)
	if err == nil && project.ID != record.ID {
		err = store.ErrConflict
	}
	return project, record.Version, err
}

func (s *Server) projectForPolicyCheckout(ctx context.Context, ref string) (model.Project, int, error) {
	canonical, err := filepath.EvalSymlinks(ref)
	if err != nil {
		return model.Project{}, 0, err
	}
	records, err := s.Store.List(ctx, "projects")
	if err != nil {
		return model.Project{}, 0, err
	}
	var project model.Project
	var version int
	for _, record := range records {
		candidate, err := store.Decode[model.Project](record)
		if err != nil {
			return model.Project{}, 0, err
		}
		if candidate.Path != canonical {
			continue
		}
		if version != 0 || candidate.ID != record.ID {
			return model.Project{}, 0, errors.New("checkout has ambiguous registration; use its project ID")
		}
		project, version = candidate, record.Version
	}
	if version == 0 {
		return project, 0, store.ErrNotFound
	}
	return project, version, nil
}
