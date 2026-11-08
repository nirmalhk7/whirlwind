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
	"whirlwind/internal/projectregistry"
	"whirlwind/internal/workflow"
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
	var project model.Project
	var err error
	if q.Engine.Agentic(q.r.Context()) {
		project, err = q.agenticProject(input)
	} else {
		project, err = input.project(q.r.Context())
	}
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
	return projectregistry.Root(ctx, path)
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
	if q.Engine.Agentic(q.r.Context()) {
		return q.registerAgenticWebhook(p)
	}
	if p.WebhookURL == "" {
		return nil
	}
	if err := validateProjectWebhookURL(p.WebhookURL); err != nil {
		return err
	}
	return q.Engine.GitHub.RegisterWebhook(q.r.Context(), p.Repo, p.WebhookURL, q.Secret)
}

func validateProjectWebhookURL(webhookURL string) error {
	u, err := url.Parse(webhookURL)
	if err != nil {
		return &apiError{422, "invalid_input", "Webhook URL must be HTTPS ending /webhooks/github"}
	}
	if u.Scheme != "https" || u.Host == "" || u.Path != "/webhooks/github" {
		return &apiError{422, "invalid_input", "Webhook URL must be HTTPS ending /webhooks/github"}
	}
	return nil
}

func (q apiRequest) registerAgenticWebhook(p model.Project) error {
	if p.WebhookURL == "" {
		return nil
	}
	return &apiError{422, "agentic_webhook_disabled", "Agentic mode uses integration context; GitHub ready-issue webhook is unavailable"}
}

func (q apiRequest) agenticProject(input projectInput) (model.Project, error) {
	ctx := q.r.Context()
	path, canonical, err := agenticRepository(ctx, input.Path)
	if err != nil {
		return model.Project{}, err
	}
	if err = initializeProject(ctx, path); err != nil {
		return model.Project{}, err
	}
	rules, hash, err := policy.LoadForOnboarding(path)
	if err != nil {
		return model.Project{}, err
	}
	sum := sha256.Sum256([]byte(canonical))
	p := model.Project{ID: hex.EncodeToString(sum[:8]), Name: input.Name, Path: path, Repo: canonical, RemoteURL: canonical, Policy: rules, PolicyHash: hash, WebhookURL: input.WebhookURL}
	p.DefaultBranch, err = q.Engine.InspectIntegrationRepository(ctx, p)
	return p, err
}

func agenticRepository(ctx context.Context, inputPath string) (string, string, error) {
	path, err := repositoryRoot(ctx, inputPath)
	if err != nil {
		return "", "", err
	}
	remote, err := github.Run(ctx, path, "git", "remote", "get-url", "origin")
	if err != nil {
		return "", "", err
	}
	canonical, err := workflow.CanonicalRemote(remote)
	return path, canonical, err
}
