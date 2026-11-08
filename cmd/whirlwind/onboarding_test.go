package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"whirlwind/internal/github"
	"whirlwind/internal/httpapi"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/testutil/openshell"
	"whirlwind/internal/workflow"
)

const onboardingReply = `[{"category":"expand","title":"Add completion alerts","problem":"External clients cannot detect completed local runs","mechanism":"Emit a bounded completion event through the existing local event stream","components":["internal/control"],"description":"Notify external tools when work finishes","scope":["internal/control"],"expected_benefit":"Avoid repeated work","effort_estimate":"Small scoped change","uncertainty":["Validate workload"],"problem_kind":"opportunity","evidence":[{"kind":"code","ref":"main.go"}]},{"category":"optimize","title":"Cache file indexes","problem":"Repeated repository scans recompute unchanged file indexes","mechanism":"Reuse a fingerprinted index for unchanged repository revisions","components":["internal/context"],"description":"Reuse unchanged repository scans","scope":["internal/context"],"expected_benefit":"Avoid repeated work","effort_estimate":"Small scoped change","uncertainty":["Validate workload"],"problem_kind":"opportunity","evidence":[{"kind":"code","ref":"main.go"}]},{"category":"harden","title":"Bound cancellation","problem":"Abandoned worker processes may outlive cancellation","mechanism":"Stop and verify the owned process group after cancellation","components":["internal/process"],"description":"Terminate abandoned worker processes","scope":["internal/process"],"expected_benefit":"Avoid repeated work","effort_estimate":"Small scoped change","uncertainty":["Validate workload"],"problem_kind":"opportunity","evidence":[{"kind":"code","ref":"main.go"}]},{"category":"simplify","title":"Unify provider setup","problem":"Provider setup has duplicate registration paths","mechanism":"Route setup through one validated registration path","components":["internal/provider"],"description":"Remove duplicate registration paths","scope":["internal/provider"],"expected_benefit":"Avoid repeated work","effort_estimate":"Small scoped change","uncertainty":["Validate workload"],"problem_kind":"opportunity","evidence":[{"kind":"code","ref":"main.go"}]}]`

const (
	onboardingPolicyYAML    = ".whirlwind.yaml"
	onboardingPolicyYML     = ".whirlwind.yml"
	onboardingGitName       = "user.name=Fixture"
	onboardingGitEmail      = "user.email=fixture@localhost"
	onboardingGitSigning    = "commit.gpgsign=false"
	onboardingProvidersPath = "/providers"
	onboardingProjectsPath  = "/projects"
	onboardingSessionID     = "onboarding-session"
	onboardingTurnID        = "onboarding-turn"
	onboardingModelID       = "onboarding-test-model"
	onboardingProjectName   = "New project"
	onboardingUserFile      = "user.txt"
	onboardingBinaryFlag    = "--binary"
	onboardingCachedFlag    = "--cached"
	onboardingRevParse      = "rev-parse"
)

type onboardingFixture struct {
	server *httpapi.Server
	repo   string
	ctx    context.Context
}

// Keeps Git, SQLite, HTTP handlers, routing, and ProcessAdapter real; substitutes remote services only.
func newOnboardingFixture(t *testing.T, preferred string) onboardingFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, realGit, args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-b", "main")
	git("config", "user.name", "Fixture")
	git("config", "user.email", "fixture@localhost")
	git("config", "commit.gpgsign", "false")
	git("config", "core.hooksPath", ".git/hooks")
	policy := "checks:\n  - name: tests\n    command: [go, test, ./...]\nallowed_paths: ['**']\nrisk_rules:\n  - level: low\n    paths: ['**']\nproviders: [" + preferred + "]\n"
	writeFixtureFile(t, filepath.Join(repo, onboardingPolicyYAML), policy, 0600)
	writeFixtureFile(t, filepath.Join(repo, "main.go"), "package main\nfunc main() {}\n", 0600)
	git("add", ".")
	git("-c", onboardingGitName, "-c", onboardingGitEmail, "-c", onboardingGitSigning, "commit", "-m", "Fixture")
	git("remote", "add", "origin", "https://github.com/whirlwind-fixture/demo")
	remote := filepath.Join(root, "remote.git")
	git("clone", "--bare", repo, remote)
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	// Redirect only remote fetch to local bare fixture. Every other Git operation is unchanged.
	writeFixtureFile(t, filepath.Join(bin, "git"), "#!/bin/sh\nif [ \"$1\" = fetch ]; then exec "+quoteFixtureShell(realGit)+" fetch --no-tags "+quoteFixtureShell(remote)+" main:refs/remotes/origin/main; fi\nexec "+quoteFixtureShell(realGit)+" \"$@\"\n", 0700)
	writeFixtureFile(t, filepath.Join(bin, "gh"), `#!/bin/sh
case "$1 $2" in
  'auth status') exit 0 ;;
  'repo view')
    for arg in "$@"; do
      if [ "$arg" = defaultBranchRef ]; then printf 'main\n'; exit 0; fi
    done
    printf '{"nameWithOwner":"whirlwind-fixture/demo"}\n' ;;
  'issue list') printf '[]\n' ;;
  'api graphql') printf '{"data":{"repository":{"discussions":{"nodes":[]}}}}\n' ;;
  *) printf 'unexpected external command\n' >&2; exit 1 ;;
esac
`, 0700)
	writeFixtureFile(t, filepath.Join(bin, "qlty"), "#!/bin/sh\nif [ \"$*\" = 'config validate --no-upgrade-check' ]; then [ -s .qlty/qlty.toml ]; exit $?; fi\n[ \"$*\" = 'init --yes --no-upgrade-check' ] || exit 2\nmkdir -p .qlty\nprintf 'config_version = \"0\"\\n' > .qlty/qlty.toml\n", 0700)
	writeFixtureFile(t, filepath.Join(bin, "codegraph"), "#!/bin/sh\n[ \"$*\" = init ] || exit 2\nmkdir -p .codegraph\nprintf 'fixture index\\n' > .codegraph/codegraph.db\n", 0700)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(bin, "codex"), "#!/bin/sh\nexec "+quoteFixtureShell(exe)+" -test.run=^TestOnboardingAgentHelper$ -- codex\n", 0700)
	parentPath := os.Getenv("PATH")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+parentPath)
	s, err := store.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := initializeProviders(ctx, s, filepath.Join(root, "bridge.mjs")); err != nil {
		t.Fatal(err)
	}
	openShell := openshell.Install(t)
	openShell.EnableCommandExecution()
	t.Setenv("WHIRLWIND_OPENSHELL_CLI", openShell.Binary)
	t.Setenv("WHIRLWIND_OPENSHELL_IMAGE", "onboarding-test-image")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+openShell.Directory+string(os.PathListSeparator)+parentPath)
	providerRecord, err := s.Get(ctx, "providers", "codex")
	if err != nil {
		t.Fatal(err)
	}
	codex, err := store.Decode[model.Provider](providerRecord)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	codex.Kind = "acp"
	codex.ACPProfile = ""
	codex.Command = []string{executable, "-test.run=^TestOnboardingAgentHelper$", "--", "acp"}
	codex.Available = true
	if err := s.Put(ctx, "providers", "codex", codex, providerRecord.Version); err != nil {
		t.Fatal(err)
	}
	// Keep tests independent of locally installed external agents.
	cursorRecord, err := s.Get(ctx, "providers", "cursor")
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := store.Decode[model.Provider](cursorRecord)
	if err != nil {
		t.Fatal(err)
	}
	cursor.Available = false
	if err := s.Put(ctx, "providers", "cursor", cursor, cursorRecord.Version); err != nil {
		t.Fatal(err)
	}
	if err := initializeSettings(ctx, s); err != nil {
		t.Fatal(err)
	}
	record, err := s.Get(ctx, "settings", "global")
	if err != nil {
		t.Fatal(err)
	}
	settings := workflow.DefaultSettings()
	if err = json.Unmarshal(record.Data, &settings); err != nil {
		t.Fatal(err)
	}
	allowance := &model.BudgetPolicy{MaxAttempts: 100, MaxRuntimeSeconds: 3600, VerificationAttempts: 4, VerificationRuntimeSeconds: 60}
	settings.Admission = model.AdmissionSettings{DefaultBudget: allowance, DiscoveryBudget: allowance, Pools: map[string]model.QuotaReserve{"*": {MinRemainingPercent: 0, MaxAgeSeconds: 60, MaxConcurrent: 8}}}
	if err = s.Put(ctx, "settings", "global", settings, record.Version); err != nil {
		t.Fatal(err)
	}
	ideation := model.AgentConfig{Role: "ideate", Chain: []model.AgentChoice{{Provider: preferred, Model: onboardingModelID}}}
	if err := s.Put(ctx, "agents", "ideate", ideation, 0); err != nil {
		t.Fatal(err)
	}
	server := httpapi.New(s, workflow.New(s, github.CLI{}, filepath.Join(root, "work")), "fixture-secret")
	server.Context = ctx
	return onboardingFixture{server, repo, ctx}
}

func writeFixtureFile(t *testing.T, path, text string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), mode); err != nil {
		t.Fatal(err)
	}
}

func quoteFixtureShell(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func (f onboardingFixture) request(t *testing.T, method, path string, body any, status int) []byte {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, "http://localhost/api/v1"+path, bytes.NewReader(encoded)).WithContext(f.ctx)
	request.Header.Set("Idempotency-Key", store.ID())
	response := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(response, request)
	if response.Code != status {
		t.Fatalf("%s %s: %d %s", method, path, response.Code, response.Body)
	}
	return response.Body.Bytes()
}

func TestGoOnboardingRunsRegisteredProvider(t *testing.T) {
	for _, kind := range []string{"codex", "acp"} {
		t.Run(kind, func(t *testing.T) {
			chosen := "codex"
			if kind == "acp" {
				chosen = "fixture"
			}
			f := newOnboardingFixture(t, chosen)
			f.verifyProvider(t, kind, chosen)
			var projectRecord model.Record
			if err := json.Unmarshal(f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": "Fixture project", "path": f.repo}, 201), &projectRecord); err != nil {
				t.Fatal(err)
			}
			project, err := store.Decode[model.Project](projectRecord)
			if err != nil || project.Repo != "whirlwind-fixture/demo" || project.DefaultBranch != "main" || project.PolicyHash == "" {
				t.Fatalf("project setup lost: %+v, %v", project, err)
			}
			f.verifyIdeation(t, project, chosen)
		})
	}
}

func (f onboardingFixture) verifyProvider(t *testing.T, kind, chosen string) {
	t.Helper()
	expectedKind := kind
	if kind == "codex" {
		expectedKind = "acp"
	}
	if kind == "acp" {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		f.request(t, "POST", onboardingProvidersPath, model.Provider{ID: chosen, Name: "Fixture ACP", Kind: "acp", Command: []string{exe, "-test.run=^TestOnboardingAgentHelper$", "--", "acp"}, Capabilities: []string{"ideate"}}, 201)
	}
	var inventory []model.Record
	if err := json.Unmarshal(f.request(t, "GET", onboardingProvidersPath, nil, 200), &inventory); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, record := range inventory {
		p, err := store.Decode[model.Provider](record)
		if err != nil {
			t.Fatal(err)
		}
		if p.ID == chosen {
			found = p.Available && p.Kind == expectedKind
		}
	}
	if !found {
		t.Fatal("registered provider missing/unavailable")
	}
}
