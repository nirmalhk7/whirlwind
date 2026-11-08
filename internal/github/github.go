package github

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"whirlwind/internal/model"
	"whirlwind/internal/process"
)

const (
	jsonFlag    = "--json"
	labelFlag   = "--label"
	limitFlag   = "--limit"
	repoFlag    = "--repo"
	stateFlag   = "--state"
	readyLabel  = "whirlwind:ready"
	issueFields = "number,title,body,updatedAt,state,url,labels"
)

// ValidSignature compares the webhook body HMAC with the sha256 signature using a constant-time comparison.
func ValidSignature(secret, sig string, body []byte) bool {
	if secret == "" || !strings.HasPrefix(sig, "sha256=") {
		return false
	}
	got, e := hex.DecodeString(strings.TrimPrefix(sig, "sha256="))
	if e != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

type Client interface {
	// Issues returns open whirlwind:ready issues for reconciliation.
	Issues(context.Context, string) ([]model.Issue, error)
	// Issue returns the current state and content of one repository issue.
	Issue(context.Context, string, int) (model.Issue, error)
	// Sources returns repository issues and available discussions for ideation.
	Sources(context.Context, string) (any, error)
	// CreateIssue publishes a plan issue or returns an existing issue with its marker.
	CreateIssue(context.Context, string, string, string, string) (model.Issue, error)
	// CreatePR publishes an implementation branch or returns its existing pull request.
	CreatePR(context.Context, string, string, string, string, string, string) (string, error)
	// RegisterWebhook ensures the repository has issue delivery configured for the URL and secret.
	RegisterWebhook(context.Context, string, string, string) error
	// PRState returns whether a published pull request is open, closed, or merged.
	PRState(context.Context, string, string) (string, error)
}
type CLI struct{}

// Run executes a registered cancellable subprocess in cwd and returns combined output with bounded error diagnostics.
func Run(ctx context.Context, cwd, name string, args ...string) (string, error) {
	cmd := process.Command(ctx, name, args...)
	cmd.Dir = cwd
	var buffer bytes.Buffer
	cmd.Stdout = &buffer
	cmd.Stderr = &buffer
	e := process.Run(cmd)
	b := buffer.Bytes()
	if e != nil {
		return string(b), fmt.Errorf("%s: %w: %.4000s", name, e, b)
	}
	return strings.TrimRight(string(b), "\r\n"), nil
}

// gh runs the GitHub CLI with the inherited login and environment.
func gh(ctx context.Context, args ...string) (string, error) { return Run(ctx, "", "gh", args...) }

// Check requires authenticated gh and a github.com origin, then resolves the repository canonical owner/name.
func Check(ctx context.Context, path string) (string, error) {
	if err := checkLocalRepository(ctx, path); err != nil {
		return "", err
	}
	remote, err := Run(ctx, path, "git", "remote", "get-url", "origin")
	if err != nil {
		return "", err
	}
	repo, err := repositoryFromRemote(remote)
	if err != nil {
		return "", err
	}
	return canonicalRepository(ctx, repo)
}

// parseIssue converts GitHub CLI issue JSON and label objects into the internal issue snapshot.
func parseIssue(b []byte) (model.Issue, error) {
	var x struct {
		Number    int    `json:"number"`
		Title     string `json:"title"`
		Body      string `json:"body"`
		UpdatedAt string `json:"updatedAt"`
		State     string `json:"state"`
		URL       string `json:"url"`
		Labels    []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	e := json.Unmarshal(b, &x)
	i := model.Issue{Number: x.Number, Title: x.Title, Body: x.Body, UpdatedAt: x.UpdatedAt, State: x.State, URL: x.URL}
	for _, l := range x.Labels {
		i.Labels = append(i.Labels, l.Name)
	}
	return i, e
}

// parseIssues decodes a GitHub issue list using the same label conversion as individual issues.
func parseIssues(text string) ([]model.Issue, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return nil, err
	}
	issues := []model.Issue{}
	for _, data := range raw {
		issue, err := parseIssue(data)
		if err != nil {
			return nil, err
		}
		issues = append(issues, issue)
	}
	return issues, nil
}

// Issues loads up to 1,000 open whirlwind:ready issues for reconciliation.
func (c CLI) Issues(ctx context.Context, repo string) ([]model.Issue, error) {
	b, e := ghRead(ctx, "issue", "list", repoFlag, repo, stateFlag, "open", labelFlag, readyLabel, limitFlag, "1000", jsonFlag, issueFields)
	if e != nil {
		return nil, e
	}
	return parseIssues(b)
}

// Issue fetches one issue snapshot, including its state, labels, and update timestamp.
func (c CLI) Issue(ctx context.Context, repo string, n int) (model.Issue, error) {
	b, e := ghRead(ctx, "issue", "view", fmt.Sprint(n), repoFlag, repo, jsonFlag, issueFields)
	if e != nil {
		return model.Issue{}, e
	}
	return parseIssue([]byte(b))
}

// Sources collects issues and attempts to fetch discussions, reporting discussion availability without failing the issue collection.
func (c CLI) Sources(ctx context.Context, repo string) (any, error) {
	issues, e := ghRead(ctx, "issue", "list", repoFlag, repo, limitFlag, "100", jsonFlag, "number,title,body,labels")
	if e != nil {
		return nil, e
	}
	parts := strings.Split(repo, "/")
	query := fmt.Sprintf(`query { repository(owner: %q,name: %q) { discussions(first: 30) { nodes { title body url } } } }`, parts[0], parts[1])
	discussions, de := ghRead(ctx, "api", "graphql", "-f", "query="+query)
	return map[string]any{"issues": json.RawMessage(issues), "discussions": discussions, "discussions_available": de == nil}, nil
}

// ensureLabel creates or updates a Whirlwind label before publishing an issue or PR.
func ensureLabel(ctx context.Context, repo, name string) error {
	_, e := gh(ctx, "label", "create", name, repoFlag, repo, "--force", "--color", "596579")
	return e
}

// CreateIssue reuses an issue containing the plan marker, or publishes a labeled issue from a temporary body file.
func (c CLI) CreateIssue(ctx context.Context, repo, title, body, marker string) (model.Issue, error) {
	issue, found, e := existingIssue(ctx, repo, title, body, marker)
	if e != nil || found {
		return issue, e
	}
	if e = ensureLabel(ctx, repo, readyLabel); e != nil {
		return model.Issue{}, e
	}
	filename, cleanup, e := publicationBodyFile("whirlwind-issue-*.md", body+"\n\n"+marker)
	if e != nil {
		return model.Issue{}, e
	}
	defer cleanup()
	url, e := gh(ctx, "issue", "create", repoFlag, repo, "--title", title, "--body-file", filename, labelFlag, readyLabel)
	if e != nil {
		return model.Issue{}, e
	}
	return c.issueAtURL(ctx, repo, url)
}

// CreatePR reuses a PR for the branch, or pushes the branch and publishes a whirlwind:work PR from a temporary body file.
func (c CLI) CreatePR(ctx context.Context, path, repo, branch, base, title, body string) (string, error) {
	url, found, e := existingPR(ctx, path, repo, branch, base, title, body)
	if e != nil || found {
		return url, e
	}
	if _, e = Run(ctx, path, "git", "push", "--set-upstream", "origin", branch); e != nil {
		return "", e
	}
	if e = ensureLabel(ctx, repo, "whirlwind:work"); e != nil {
		return "", e
	}
	filename, cleanup, e := publicationBodyFile("whirlwind-pr-*.md", body)
	if e != nil {
		return "", e
	}
	defer cleanup()
	return gh(ctx, "pr", "create", repoFlag, repo, "--head", branch, "--base", base, "--title", title, "--body-file", filename, labelFlag, "whirlwind:work")
}

// RegisterWebhook reuses a hook with the same URL, or registers an issue webhook with its secret and TLS verification.
func (c CLI) RegisterWebhook(ctx context.Context, repo, url, secret string) error {
	if secret == "" {
		return errors.New("webhook secret required")
	}
	found, e := existingWebhook(ctx, repo, url)
	if e != nil || found {
		return e
	}
	payload := map[string]any{"name": "web", "active": true, "events": []string{"issues"}, "config": map[string]any{"url": url, "content_type": "json", "secret": secret, "insecure_ssl": "0"}}
	filename, cleanup, e := publicationFile("whirlwind-hook-*.json", func(f *os.File) error {
		return json.NewEncoder(f).Encode(payload)
	})
	if e != nil {
		return e
	}
	defer cleanup()
	_, e = gh(ctx, "api", "repos/"+repo+"/hooks", "--method", "POST", "--input", filename)
	return e
}

// PRState fetches the published PR state so reconciliation can record closure or merge.
func (c CLI) PRState(ctx context.Context, repo, url string) (string, error) {
	b, e := ghRead(ctx, "pr", "view", url, repoFlag, repo, jsonFlag, "state")
	var v struct {
		State string `json:"state"`
	}
	if e != nil {
		return "", e
	}
	e = json.Unmarshal([]byte(b), &v)
	return v.State, e
}

// DefaultBranch asks GitHub for the repository default branch name.
func DefaultBranch(ctx context.Context, repo string) (string, error) {
	return ghRead(ctx, "repo", "view", repo, jsonFlag, "defaultBranchRef", "--jq", ".defaultBranchRef.name")
}
