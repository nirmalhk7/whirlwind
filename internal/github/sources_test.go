package github

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSourcesResolvesRepositoryIdentityForDiscussions(t *testing.T) {
	for _, repo := range []string{"o/r", "https://github.com/o/r", "https://github.com/o/r.git", "https://github.com/o/r/"} {
		t.Run(repo, func(t *testing.T) {
			installSourcesCLI(t)
			sources, err := (CLI{}).Sources(context.Background(), repo)
			if err != nil {
				t.Fatal(err)
			}
			result := sources.(map[string]any)
			if result["discussions_available"] != true || result["discussions"] != `{"data":{"repository":{"discussions":{"nodes":[{"title":"Research"}]}}}}` {
				t.Fatalf("repository discussions unavailable: %+v", result)
			}
			if string(result["issues"].(json.RawMessage)) != `[{"number":1,"title":"Boundary"}]` {
				t.Fatalf("issues lost: %+v", result)
			}
		})
	}
}

func TestSourcesPreservesIssuesWhenDiscussionsUnavailable(t *testing.T) {
	installSourcesCLI(t)
	t.Setenv("WHIRLWIND_DISCUSSIONS_DISABLED", "1")
	sources, err := (CLI{}).Sources(context.Background(), "https://github.com/o/r")
	if err != nil {
		t.Fatal(err)
	}
	result := sources.(map[string]any)
	if result["discussions_available"] != false || string(result["issues"].(json.RawMessage)) != `[{"number":1,"title":"Boundary"}]` {
		t.Fatalf("unavailable discussions broke issue collection: %+v", result)
	}
}

func installSourcesCLI(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
case "$1 $2" in
"issue list") printf '[{"number":1,"title":"Boundary"}]' ;;
"repo view") printf '{"nameWithOwner":"o/r"}' ;;
"api graphql")
  if [ "$WHIRLWIND_DISCUSSIONS_DISABLED" = "1" ]; then echo 'Discussions disabled' >&2; exit 1; fi
  if [ "$4" != 'query=query { repository(owner: "o",name: "r") { discussions(first: 30) { nodes { title body url } } } }' ]; then
    echo 'Wrong repository identity' >&2; exit 1
  fi
  printf '{"data":{"repository":{"discussions":{"nodes":[{"title":"Research"}]}}}}' ;;
*) exit 2 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}
