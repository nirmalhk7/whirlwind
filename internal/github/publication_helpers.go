package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"whirlwind/internal/model"
)

func checkLocalRepository(ctx context.Context, path string) error {
	if _, err := exec.LookPath("gh"); err != nil {
		return errors.New("gh required")
	}
	if _, err := gh(ctx, "auth", "status"); err != nil {
		return err
	}
	_, err := Run(ctx, path, "git", "rev-parse", "--show-toplevel")
	return err
}

func repositoryFromRemote(remote string) (string, error) {
	remote = strings.TrimSuffix(remote, ".git")
	var repo string
	switch {
	case strings.HasPrefix(remote, "git@github.com:"):
		repo = strings.TrimPrefix(remote, "git@github.com:")
	case strings.HasPrefix(remote, "https://github.com/"):
		repo = strings.TrimPrefix(remote, "https://github.com/")
	default:
		return "", errors.New("origin must reference github.com repository")
	}
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", errors.New("invalid GitHub repository")
	}
	return repo, nil
}

func canonicalRepository(ctx context.Context, repo string) (string, error) {
	description, err := gh(ctx, "repo", "view", repo, jsonFlag, "nameWithOwner")
	if err != nil {
		return "", err
	}
	var canonical struct {
		Name string `json:"nameWithOwner"`
	}
	if err = json.Unmarshal([]byte(description), &canonical); err != nil || canonical.Name == "" {
		return "", errors.New("GitHub repository identity unavailable")
	}
	return canonical.Name, nil
}

func existingIssue(ctx context.Context, repo, title, body, marker string) (model.Issue, bool, error) {
	all, e := gh(ctx, "issue", "list", repoFlag, repo, stateFlag, "all", limitFlag, "1000", jsonFlag, issueFields)
	if e != nil {
		return model.Issue{}, false, e
	}
	issues, e := parseIssues(all)
	if e != nil {
		return model.Issue{}, false, e
	}
	for _, i := range issues {
		if strings.Contains(i.Body, marker) {
			if i.Title != title || strings.TrimSpace(i.Body) != strings.TrimSpace(body+"\n\n"+marker) {
				return model.Issue{}, false, errors.New("existing issue does not match publication intent")
			}
			return i, true, nil
		}
	}
	return model.Issue{}, false, nil
}

func publicationFile(pattern string, write func(*os.File) error) (string, func(), error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.Remove(f.Name()) }
	err = write(f)
	_ = f.Close()
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return f.Name(), cleanup, nil
}

type publishedPR struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	Body  string `json:"body"`
	Base  string `json:"baseRefName"`
	Head  string `json:"headRefOid"`
}

func existingPR(ctx context.Context, path, repo, branch, base, title, body string) (string, bool, error) {
	b, e := gh(ctx, "pr", "list", repoFlag, repo, "--head", branch, stateFlag, "all", jsonFlag, "url,title,body,baseRefName,headRefOid")
	if e != nil {
		return "", false, e
	}
	var found []publishedPR

	if e = json.Unmarshal([]byte(b), &found); e != nil {
		return "", false, e
	}
	if len(found) == 0 {
		return "", false, nil
	}
	url, err := matchExistingPR(ctx, path, found[0], base, title, body)
	return url, true, err
}

func matchExistingPR(ctx context.Context, path string, found publishedPR, base, title, body string) (string, error) {
	head, err := Run(ctx, path, "git", "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if found.Title != title || found.Body != body || found.Base != base || found.Head != head {
		return "", errors.New("existing PR does not match publication intent")
	}
	return found.URL, nil
}

func existingWebhook(ctx context.Context, repo, url string) (bool, error) {
	b, e := gh(ctx, "api", "repos/"+repo+"/hooks")
	if e != nil {
		return false, e
	}
	var hooks []struct {
		Config struct {
			URL string `json:"url"`
		} `json:"config"`
	}
	if e = json.Unmarshal([]byte(b), &hooks); e != nil {
		return false, e
	}
	for _, h := range hooks {
		if h.Config.URL == url {
			return true, nil
		}
	}
	return false, nil
}

func publicationBodyFile(pattern, body string) (string, func(), error) {
	return publicationFile(pattern, func(f *os.File) error {
		_, err := f.WriteString(body)
		return err
	})
}

func (c CLI) issueAtURL(ctx context.Context, repo, url string) (model.Issue, error) {
	var n int
	_, e := fmt.Sscanf(url[strings.LastIndex(url, "/")+1:], "%d", &n)
	if e != nil {
		return model.Issue{}, e
	}
	return c.Issue(ctx, repo, n)
}
