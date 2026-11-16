package github

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

func discussionSources(ctx context.Context, repo string) (string, error) {
	canonical, err := canonicalRepository(ctx, repo)
	if err != nil {
		return "", err
	}
	owner, name, ok := strings.Cut(canonical, "/")
	if !ok || owner == "" || name == "" {
		return "", errors.New("invalid GitHub repository identity")
	}
	query := fmt.Sprintf(`query { repository(owner: %q,name: %q) { discussions(first: 30) { nodes { title body url } } } }`, owner, name)
	return ghRead(ctx, "api", "graphql", "-f", "query="+query)
}
