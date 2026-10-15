package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"path/filepath"

	"whirlwind/internal/github"
	"whirlwind/internal/model"
	"whirlwind/internal/policy"
)

type projectInput struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	WebhookURL string `json:"webhook_url"`
}

func (q apiRequest) createProject() error {
	var input projectInput
	if err := json.Unmarshal(q.body, &input); err != nil {
		return err
	}
	if input.Name == "" || !filepath.IsAbs(input.Path) {
		return &apiError{422, "invalid_input", "Name and absolute repository path required"}
	}
	project, err := input.project(q.r.Context())
	if err != nil {
		return err
	}
	if err = q.registerProjectWebhook(project); err != nil {
		return err
	}
	q.save(q.w, q.r, "projects", project.ID, project, 0, 201)
	return nil
}

func repositoryRoot(ctx context.Context, path string) (string, error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	topLevel, err := github.Run(ctx, canonical, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(topLevel)
}

func (input projectInput) project(ctx context.Context) (model.Project, error) {
	var project model.Project
	canonical, err := repositoryRoot(ctx, input.Path)
	if err != nil {
		return project, err
	}
	repo, branch, err := checkedRepository(ctx, canonical)
	if err != nil {
		return project, err
	}
	if err = initializeProject(ctx, canonical); err != nil {
		return project, err
	}
	p, h, err := policy.LoadForOnboarding(canonical)
	if err != nil {
		return project, err
	}
	sum := sha256.Sum256([]byte(repo))
	project = model.Project{ID: hex.EncodeToString(sum[:8]), Name: input.Name, Repo: repo, DefaultBranch: branch, Path: canonical, Policy: p, PolicyHash: h, WebhookURL: input.WebhookURL}
	return project, nil
}

func checkedRepository(ctx context.Context, path string) (string, string, error) {
	repo, err := github.Check(ctx, path)
	if err != nil {
		return "", "", err
	}
	branch, err := github.DefaultBranch(ctx, repo)
	return repo, branch, err
}

func (q apiRequest) registerProjectWebhook(p model.Project) error {
	if p.WebhookURL == "" {
		return nil
	}
	u, err := url.Parse(p.WebhookURL)
	if err != nil {
		return &apiError{422, "invalid_input", "Webhook URL must be HTTPS ending /webhooks/github"}
	}
	if u.Scheme != "https" || u.Host == "" || u.Path != "/webhooks/github" {
		return &apiError{422, "invalid_input", "Webhook URL must be HTTPS ending /webhooks/github"}
	}
	return q.Engine.GitHub.RegisterWebhook(q.r.Context(), p.Repo, p.WebhookURL, q.Secret)
}
