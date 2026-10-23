package workflow

import (
	"errors"
	"net/url"
	"strings"

	"whirlwind/internal/github"
	"whirlwind/internal/model"
)

func (step *taskStep) pushIntegrationCommit(intent model.Publication) error {
	if intent.Head == "" {
		return errors.New("verified immutable commit required before publication push")
	}
	raw, err := github.Run(step.ctx, step.task.Worktree, "git", "remote", "get-url", "--push", "--all", "origin")
	if err != nil {
		return err
	}
	destination, err := integrationPushDestination(raw, step.project.RemoteURL)
	if err != nil {
		return err
	}
	// Push the validated URL directly so a later origin edit cannot redirect it.
	_, err = github.Run(step.ctx, step.task.Worktree, "git", "push", "--set-upstream", "--", destination, intent.Head+":refs/heads/"+intent.Branch)
	return err
}

func integrationPushDestination(raw, repository string) (string, error) {
	destinations := strings.Fields(raw)
	if len(destinations) != 1 {
		return "", errors.New("publication requires exactly one effective origin push URL; configure a single destination matching the canonical repository")
	}
	destination := destinations[0]
	if !hostedIntegrationPushURL(destination) {
		return "", errors.New("publication push destination must use a supported hosted Git URL; local paths and helper transports are forbidden")
	}
	canonical, err := CanonicalRemote(destination)
	if err != nil || canonical != repository {
		return "", errors.New("publication push destination does not match the configured canonical repository; restore origin before retrying")
	}
	return destination, nil
}

func hostedIntegrationPushURL(destination string) bool {
	if strings.HasPrefix(destination, "git@") {
		return true
	}
	parsed, err := url.Parse(destination)
	if err != nil || parsed.Hostname() == "" {
		return false
	}
	switch parsed.Scheme {
	case "https", "http", "ssh", "git":
		return true
	default:
		return false
	}
}
