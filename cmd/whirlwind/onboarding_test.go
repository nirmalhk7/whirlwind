package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	"whirlwind/internal/policy"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/internal/workflow"
)

const onboardingReply = `[{"category":"expand","title":"Add completion alerts","description":"Notify external tools when work finishes"},{"category":"optimize","title":"Cache file indexes","description":"Reuse unchanged repository scans"},{"category":"harden","title":"Bound cancellation","description":"Terminate abandoned worker processes"},{"category":"simplify","title":"Unify provider setup","description":"Remove duplicate registration paths"}]`

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
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	s, err := store.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := initializeProviders(ctx, s, filepath.Join(root, "bridge.mjs")); err != nil {
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
			found = p.Available && p.Kind == kind
		}
	}
	if !found {
		t.Fatal("registered provider missing/unavailable")
	}
}

func (f onboardingFixture) verifyIdeation(t *testing.T, project model.Project, chosen string) {
	t.Helper()
	var queued struct {
		ID string `json:"operation_id"`
	}
	if err := json.Unmarshal(f.request(t, "POST", "/projects/"+project.ID+"/ideation", map[string]any{}, 202), &queued); err != nil || queued.ID == "" {
		t.Fatalf("ideation was not queued: %+v, %v", queued, err)
	}
	waitOnboardingOperation(t, f, queued.ID)
	var directions []model.Record
	if err := json.Unmarshal(f.request(t, "GET", "/directions", nil, 200), &directions); err != nil || len(directions) != 4 {
		t.Fatalf("agent reply not applied: %d directions, %v", len(directions), err)
	}
	var sessions []model.Record
	if err := json.Unmarshal(f.request(t, "GET", "/sessions", nil, 200), &sessions); err != nil || len(sessions) != 2 {
		t.Fatalf("agent session not persisted: %d sessions, %v", len(sessions), err)
	}
	var result struct {
		Provider  string `json:"provider"`
		SessionID string `json:"session_id"`
		Summary   string `json:"summary"`
		Finished  bool   `json:"finished"`
	}
	summaries := map[string]bool{}
	for _, session := range sessions {
		if err := json.Unmarshal(session.Data, &result); err != nil || result.Provider != chosen || result.SessionID != onboardingSessionID || !result.Finished {
			t.Fatalf("final reply/session lost: %+v, %v", result, err)
		}
		summaries[result.Summary] = true
	}
	if !summaries[onboardingReply] || !summaries[`{"unique":true}`] {
		t.Fatalf("generation or novelty review missing: %v", summaries)
	}
}

func waitOnboardingOperation(t *testing.T, f onboardingFixture, id string) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		record, err := f.server.Store.Get(f.ctx, "operations", id)
		if err != nil {
			t.Fatal(err)
		}
		var operation struct{ Stage, Error string }
		if err := json.Unmarshal(record.Data, &operation); err != nil {
			t.Fatal(err)
		}
		if operation.Stage == "complete" {
			return
		}
		if operation.Stage == "failed" {
			t.Fatal("onboarded agent failed:", operation.Error)
		}
		select {
		case <-f.ctx.Done():
			t.Fatal("onboarding timed out:", f.ctx.Err())
		case <-ticker.C:
		}
	}
}

func TestGoOnboardingRejectsInvalidSetup(t *testing.T) {
	f := newOnboardingFixture(t, "codex")
	f.request(t, "POST", onboardingProvidersPath, model.Provider{ID: "invalid", Name: "Invalid", Kind: "codex", Command: []string{"codex"}}, 422)
	f.request(t, "POST", onboardingProvidersPath, model.Provider{ID: "empty", Name: "Empty", Kind: "acp"}, 422)
	var missing model.Record
	if err := json.Unmarshal(f.request(t, "POST", onboardingProvidersPath, model.Provider{ID: "missing", Name: "Missing", Kind: "acp", Command: []string{filepath.Join(t.TempDir(), "missing-agent")}}, 201), &missing); err != nil {
		t.Fatal(err)
	}
	p, err := store.Decode[model.Provider](missing)
	if err != nil || p.Available {
		t.Fatalf("missing executable marked available: %+v, %v", p, err)
	}
	policyPath := filepath.Join(f.repo, onboardingPolicyYAML)
	committed, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, policyPath, string(committed)+"# uncommitted\n", 0600)
	f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": "Uncommitted", "path": f.repo}, 422)
	if err := os.Remove(policyPath); err != nil {
		t.Fatal(err)
	}
	f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": "Missing policy", "path": f.repo}, 422)
}

func TestProjectSetupCreatesCommittedExecutionPolicy(t *testing.T) {
	f := newOnboardingFixture(t, "codex")
	committed, err := os.ReadFile(filepath.Join(f.repo, onboardingPolicyYAML))
	if err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		if out, err := github.Run(f.ctx, f.repo, "git", args...); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	git("rm", onboardingPolicyYAML)
	git("-c", onboardingGitName, "-c", onboardingGitEmail, "-c", onboardingGitSigning, "commit", "-m", "Remove policy")
	body := f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": onboardingProjectName, "path": f.repo}, 201)
	starter, err := os.ReadFile(filepath.Join(f.repo, onboardingPolicyYML))
	if err != nil {
		t.Fatal("starter policy not created:", err)
	}
	verifyInitializedOnboardingProject(t, f, body)
	// Retrying must preserve the draft and avoid reinitializing configured tools.
	bin := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))[0]
	for _, tool := range []string{"qlty", "codegraph"} {
		script := "#!/bin/sh\nexit 1\n"
		if tool == "qlty" {
			script = "#!/bin/sh\n[ \"$*\" = 'config validate --no-upgrade-check' ] && exit 0\nexit 1\n"
		}
		writeFixtureFile(t, filepath.Join(bin, tool), script, 0700)
	}
	f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": onboardingProjectName, "path": f.repo}, 409)
	if current, err := os.ReadFile(filepath.Join(f.repo, onboardingPolicyYML)); err != nil || !bytes.Equal(current, starter) {
		t.Fatal("starter overwritten on retry")
	}
	writeFixtureFile(t, filepath.Join(f.repo, onboardingPolicyYML), string(committed), 0600)
	f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": onboardingProjectName, "path": f.repo}, 422)
	git("add", onboardingPolicyYML, ".qlty/qlty.toml")
	git("-c", onboardingGitName, "-c", onboardingGitEmail, "-c", onboardingGitSigning, "commit", "-m", "Configure project")
	f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": onboardingProjectName, "path": f.repo}, 409)
}

func TestProjectSetupStopsOnToolFailure(t *testing.T) {
	for _, tool := range []string{"qlty", "codegraph"} {
		t.Run(tool, func(t *testing.T) {
			f := newOnboardingFixture(t, "codex")
			bin := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))[0]
			writeFixtureFile(t, filepath.Join(bin, tool), "#!/bin/sh\nprintf 'initialization failed\\n' >&2\nexit 1\n", 0700)
			body := f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": "Project", "path": f.repo}, 422)
			if !bytes.Contains(body, []byte(tool)) {
				t.Fatalf("missing failed tool: %s", body)
			}
			if records, err := f.server.Store.List(f.ctx, "projects"); err != nil || len(records) != 0 {
				t.Fatalf("failed setup registered: %v %v", records, err)
			}
		})
	}
}

func TestProviderStartupPreservesExistingConfiguration(t *testing.T) {
	f := newOnboardingFixture(t, "codex")
	record, err := f.server.Store.Get(f.ctx, "providers", "codex")
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.Decode[model.Provider](record)
	if err != nil {
		t.Fatal(err)
	}
	p.Command = []string{"custom-codex", "app-server"}
	p.Available = false
	if err := f.server.Store.Put(f.ctx, "providers", p.ID, p, record.Version); err != nil {
		t.Fatal(err)
	}
	if err := initializeProviders(f.ctx, f.server.Store, "another-bridge"); err != nil {
		t.Fatal(err)
	}
	saved, err := f.server.Store.Get(f.ctx, "providers", "codex")
	if err != nil {
		t.Fatal(err)
	}
	current, err := store.Decode[model.Provider](saved)
	if err != nil || current.Command[0] != "custom-codex" || current.Available || saved.Version != record.Version+1 {
		t.Fatalf("startup overwrote user configuration: %+v, %v", current, err)
	}
}

func TestOnboardingAgentHelper(t *testing.T) {
	kind := onboardingAgentKind()
	if kind == "" {
		return
	}
	emit := func(value any) {
		if json.NewEncoder(os.Stdout).Encode(value) != nil {
			os.Exit(2)
		}
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var m provider.Message
		if json.Unmarshal(scanner.Bytes(), &m) != nil {
			os.Exit(2)
		}
		result := any(map[string]any{})
		switch m.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{"loadSession": true}}
		case "initialized":
			continue
		case "session/new":
			result = map[string]any{"sessionId": onboardingSessionID}
		case "thread/start":
			result = map[string]any{"thread": map[string]any{"id": onboardingSessionID}}
		case "session/prompt", "turn/start":
			if !validOnboardingPrompt(kind, m, emit) {
				continue
			}
			streamOnboardingReply(kind, m, emit)
			continue
		}
		emit(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": result})
	}
	if scanner.Err() != nil {
		fmt.Fprintln(os.Stderr, scanner.Err())
		os.Exit(2)
	}
	os.Exit(0)
}

func validOnboardingPrompt(kind string, m provider.Message, emit func(any)) bool {
	key := "prompt"
	if kind == "codex" {
		key = "input"
	}
	var params map[string]json.RawMessage
	var prompt []struct{ Text string }
	_ = json.Unmarshal(m.Params, &params)
	_ = json.Unmarshal(params[key], &prompt)
	if len(prompt) != 1 || (!strings.Contains(prompt[0].Text, "Generate exactly four") && !strings.Contains(prompt[0].Text, "Candidate ideas:")) || !strings.Contains(prompt[0].Text, "Versioned handoff:") {
		emit(map[string]any{"id": m.ID, "error": map[string]any{"code": -32602, "message": "workflow prompt missing"}})
		return false
	}
	return true
}

func streamOnboardingReply(kind string, m provider.Message, emit func(any)) {
	if kind == "codex" {
		emit(map[string]any{"id": m.ID, "result": map[string]any{"turn": map[string]any{"id": onboardingTurnID, "status": "inProgress"}}})
	}
	reply := onboardingReply
	var params map[string]json.RawMessage
	_ = json.Unmarshal(m.Params, &params)
	var content []struct{ Text string }
	key := "prompt"
	if kind == "codex" {
		key = "input"
	}
	_ = json.Unmarshal(params[key], &content)
	if len(content) == 1 && strings.Contains(content[0].Text, "Candidate ideas:") {
		reply = `{"unique":true}`
	}
	middle := len(reply) / 2
	for _, text := range []string{reply[:middle], reply[middle:]} {
		if kind == "acp" {
			emit(map[string]any{"method": "session/update", "params": map[string]any{"sessionId": onboardingSessionID, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": text}}}})
		} else {
			emit(map[string]any{"method": "item/agentMessage/delta", "params": map[string]string{"threadId": onboardingSessionID, "turnId": onboardingTurnID, "itemId": "message", "delta": text}})
		}
	}
	if kind == "codex" {
		emit(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": onboardingSessionID, "turn": map[string]any{"id": onboardingTurnID, "status": "completed", "items": []any{}}}})
		return
	}
	emit(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]string{"stopReason": "end_turn"}})
}

func onboardingAgentKind() string {
	kind := ""
	for n, arg := range os.Args {
		if arg == "--" && n+1 < len(os.Args) {
			kind = os.Args[n+1]
		}
	}
	return kind
}

func TestManualIdeationWaitsForActiveGeneration(t *testing.T) {
	f := newOnboardingFixture(t, "codex")
	var record model.Record
	if err := json.Unmarshal(f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": "Fixture", "path": f.repo}, 201), &record); err != nil {
		t.Fatal(err)
	}
	project, err := store.Decode[model.Project](record)
	if err != nil {
		t.Fatal(err)
	}
	key := "generation:" + project.ID
	lease, err := f.server.Store.Claim(f.ctx, key, "scheduled", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var queued struct {
		ID string `json:"operation_id"`
	}
	if err := json.Unmarshal(f.request(t, "POST", "/projects/"+project.ID+"/ideation", map[string]any{}, 202), &queued); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	operation, err := f.server.Store.Get(f.ctx, "operations", queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	var status struct {
		Stage string `json:"stage"`
	}
	if err := json.Unmarshal(operation.Data, &status); err != nil || status.Stage != "running" {
		t.Fatalf("manual generation did not wait: %s %v", operation.Data, err)
	}
	if err := f.server.Store.Release(f.ctx, key, "scheduled", lease.Fence); err != nil {
		t.Fatal(err)
	}
	waitOnboardingOperation(t, f, queued.ID)
}

func removeOnboardingPolicy(t *testing.T, f onboardingFixture) {
	t.Helper()
	for _, args := range [][]string{{"rm", onboardingPolicyYAML}, {"-c", onboardingGitName, "-c", onboardingGitEmail, "-c", onboardingGitSigning, "commit", "-m", "Remove policy"}} {
		if _, err := github.Run(f.ctx, f.repo, "git", args...); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOnboardingBlocksGHBeforeRepositoryWrites(t *testing.T) {
	for _, missing := range []bool{true, false} {
		t.Run(fmt.Sprint("missing=", missing), func(t *testing.T) {
			f := newOnboardingFixture(t, "codex")
			removeOnboardingPolicy(t, f)
			bin := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))[0]
			if missing {
				if err := os.Remove(filepath.Join(bin, "gh")); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", bin)
			} else {
				writeFixtureFile(t, filepath.Join(bin, "gh"), "#!/bin/sh\nprintf 'gh authentication required\n' >&2\nexit 1\n", 0700)
			}
			body := f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": "Project", "path": f.repo}, 422)
			if !bytes.Contains(body, []byte("gh")) {
				t.Fatalf("wrong preflight failure: %s", body)
			}
			assertBlockedOnboardingFiles(t, f)
		})
	}
}

func TestOnboardingCommitsOnlyNewStarterPolicy(t *testing.T) {
	f := newOnboardingFixture(t, "codex")
	removeOnboardingPolicy(t, f)
	for _, args := range [][]string{{"config", "user.name", "Fixture"}, {"config", "user.email", "fixture@localhost"}, {"config", "commit.gpgsign", "false"}} {
		if _, err := github.Run(f.ctx, f.repo, "git", args...); err != nil {
			t.Fatal(err)
		}
	}
	writeFixtureFile(t, filepath.Join(f.repo, onboardingUserFile), "staged user work\n", 0600)
	if _, err := github.Run(f.ctx, f.repo, "git", "add", onboardingUserFile); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(f.repo, onboardingUserFile), "unstaged user work\n", 0600)
	writeFixtureFile(t, filepath.Join(f.repo, "main.go"), "package main // user edit\n", 0600)
	before, err := github.Run(f.ctx, f.repo, "git", "diff", onboardingCachedFlag, onboardingBinaryFlag)
	if err != nil {
		t.Fatal(err)
	}
	f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": "Project", "path": f.repo}, 201)
	committed, err := github.Run(f.ctx, f.repo, "git", "show", "HEAD:"+onboardingPolicyYML)
	if err != nil || !strings.Contains(committed, "qlty") {
		t.Fatalf("starter not committed: %s %v", committed, err)
	}
	files, err := github.Run(f.ctx, f.repo, "git", "show", "--format=", "--name-only", "HEAD")
	if err != nil || files != ".qlty/qlty.toml\n"+onboardingPolicyYML {
		t.Fatalf("commit included other work: %q %v", files, err)
	}
	after, err := github.Run(f.ctx, f.repo, "git", "diff", onboardingCachedFlag, onboardingBinaryFlag)
	if err != nil || before != after {
		t.Fatal("user staging changed")
	}
	if content, err := os.ReadFile(filepath.Join(f.repo, onboardingUserFile)); err != nil || string(content) != "unstaged user work\n" {
		t.Fatal("user work changed")
	}
	head, _ := github.Run(f.ctx, f.repo, "git", onboardingRevParse, "HEAD")
	f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": "Project", "path": f.repo}, 409)
	next, _ := github.Run(f.ctx, f.repo, "git", onboardingRevParse, "HEAD")
	if next != head {
		t.Fatal("retry created another commit")
	}
}

func TestOnboardingValidatesQltyAndVerifiesInitialization(t *testing.T) {
	for _, existing := range []bool{true, false} {
		t.Run(fmt.Sprint("existing=", existing), func(t *testing.T) {
			f := newOnboardingFixture(t, "codex")
			bin := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))[0]
			if existing {
				if err := os.Mkdir(filepath.Join(f.repo, ".qlty"), 0700); err != nil {
					t.Fatal(err)
				}
				writeFixtureFile(t, filepath.Join(f.repo, ".qlty", "qlty.toml"), "invalid config", 0600)
				writeFixtureFile(t, filepath.Join(bin, "qlty"), "#!/bin/sh\nprintf 'invalid Qlty configuration\n' >&2\nexit 1\n", 0700)
			} else {
				writeFixtureFile(t, filepath.Join(bin, "qlty"), "#!/bin/sh\nexit 0\n", 0700)
			}
			body := f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": "Project", "path": f.repo}, 422)
			if !bytes.Contains(bytes.ToLower(body), []byte("qlty")) {
				t.Fatalf("missing Qlty failure: %s", body)
			}
			records, _ := f.server.Store.List(f.ctx, "projects")
			if len(records) != 0 {
				t.Fatal("invalid setup registered")
			}
		})
	}
}

func TestStarterCommitFailureBlocksRegistrationAndAllowsRetry(t *testing.T) {
	f := newOnboardingFixture(t, "codex")
	removeOnboardingPolicy(t, f)
	writeFixtureFile(t, filepath.Join(f.repo, onboardingUserFile), "staged work\n", 0600)
	if _, err := github.Run(f.ctx, f.repo, "git", "add", onboardingUserFile); err != nil {
		t.Fatal(err)
	}
	before, _ := github.Run(f.ctx, f.repo, "git", "diff", onboardingCachedFlag, onboardingBinaryFlag)
	writeFixtureFile(t, filepath.Join(f.repo, ".git", "hooks", "pre-commit"), "#!/bin/sh\necho 'fixture commit refused' >&2\nexit 1\n", 0700)
	body := f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": "Project", "path": f.repo}, 422)
	if !bytes.Contains(body, []byte("commit")) {
		t.Fatalf("commit failure missing: %s", body)
	}
	records, _ := f.server.Store.List(f.ctx, "projects")
	if len(records) != 0 {
		t.Fatal("failed commit registered project")
	}
	if _, err := os.Stat(filepath.Join(f.repo, onboardingPolicyYML)); !os.IsNotExist(err) {
		t.Fatalf("failed generated policy not cleaned up: %v", err)
	}
	after, _ := github.Run(f.ctx, f.repo, "git", "diff", onboardingCachedFlag, onboardingBinaryFlag)
	if before != after {
		t.Fatal("failed commit altered user staging")
	}
	if err := os.Remove(filepath.Join(f.repo, ".git", "hooks", "pre-commit")); err != nil {
		t.Fatal(err)
	}
	f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": "Project", "path": f.repo}, 201)
	if _, err := github.Run(f.ctx, f.repo, "git", "show", "HEAD:"+onboardingPolicyYML); err != nil {
		t.Fatal("retry did not commit policy:", err)
	}
}

func TestOnboardingDoesNotReplaceMissingStagedPolicy(t *testing.T) {
	f := newOnboardingFixture(t, "codex")
	removeOnboardingPolicy(t, f)
	writeFixtureFile(t, filepath.Join(f.repo, onboardingPolicyYML), "# staged user policy\n", 0600)
	if _, err := github.Run(f.ctx, f.repo, "git", "add", "--", onboardingPolicyYML); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.repo, onboardingPolicyYML)); err != nil {
		t.Fatal(err)
	}
	f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": "Project", "path": f.repo}, 422)
	staged, err := github.Run(f.ctx, f.repo, "git", "show", ":"+onboardingPolicyYML)
	if err != nil || staged != "# staged user policy" {
		t.Fatalf("staged policy overwritten: %q %v", staged, err)
	}
	if _, err = os.Stat(filepath.Join(f.repo, onboardingPolicyYML)); !os.IsNotExist(err) {
		t.Fatal("missing staged policy replaced")
	}
}

func TestStarterGitPreflightBlocksBeforeToolWrites(t *testing.T) {
	for _, state := range []string{"identity", "merge"} {
		t.Run(state, func(t *testing.T) {
			f := newOnboardingFixture(t, "codex")
			removeOnboardingPolicy(t, f)
			configureStarterGitFailure(t, f, state)
			f.request(t, "POST", onboardingProjectsPath, map[string]string{"name": "Project", "path": f.repo}, 422)
			for _, name := range []string{onboardingPolicyYML, ".qlty", ".codegraph"} {
				if _, err := os.Stat(filepath.Join(f.repo, name)); !os.IsNotExist(err) {
					t.Fatalf("Git preflight failure wrote %s", name)
				}
			}
		})
	}
}

func TestPolicySetupRepairsOnlyGeneratedLegacyStarter(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Run(fmt.Sprint("custom=", custom), func(t *testing.T) {
			f := newOnboardingFixture(t, "codex")
			removeOnboardingPolicy(t, f)
			legacy := prepareLegacyOnboardingPolicy(t, f, custom)
			status := 200
			if custom {
				status = 422
			}
			request := httptest.NewRequest("POST", "http://localhost/api/v1/projects/p/policy", strings.NewReader("{}")).WithContext(f.ctx)
			request.Header.Set("Idempotency-Key", store.ID())
			request.Header.Set("If-Match", "1")
			response := httptest.NewRecorder()
			f.server.Handler().ServeHTTP(response, request)
			if response.Code != status {
				t.Fatalf("policy setup: %d %s", response.Code, response.Body)
			}
			body := response.Body.Bytes()
			if custom {
				if content, _ := os.ReadFile(filepath.Join(f.repo, onboardingPolicyYML)); string(content) != legacy {
					t.Fatal("custom policy overwritten")
				}
				return
			}
			assertUpgradedOnboardingPolicy(t, f, body)
		})
	}
}

func verifyInitializedOnboardingProject(t *testing.T, f onboardingFixture, body []byte) {
	t.Helper()
	var record model.Record
	if err := json.Unmarshal(body, &record); err != nil {
		t.Fatal(err)
	}
	project, err := store.Decode[model.Project](record)
	if err != nil || project.PolicyHash == "" || len(project.Policy.AllowedPaths) == 0 {
		t.Fatalf("onboarding left execution unconfigured: %+v %v", project, err)
	}
	f.verifyIdeation(t, project, "codex")
	if _, hash, err := policy.Load(f.repo); err != nil || hash != project.PolicyHash {
		t.Fatalf("execution policy not loadable: %s %v", hash, err)
	}
	for _, name := range []string{".qlty/qlty.toml", ".codegraph/codegraph.db"} {
		if _, err := os.Stat(filepath.Join(f.repo, name)); err != nil {
			t.Fatal("project tool not initialized:", err)
		}
	}
	if records, err := f.server.Store.List(f.ctx, "projects"); err != nil || len(records) != 1 {
		t.Fatalf("ideation project missing: %v %v", records, err)
	}
}

func assertBlockedOnboardingFiles(t *testing.T, f onboardingFixture) {
	t.Helper()
	for _, name := range []string{onboardingPolicyYML, ".qlty", ".codegraph"} {
		if _, err := os.Stat(filepath.Join(f.repo, name)); !os.IsNotExist(err) {
			t.Fatalf("blocked onboarding wrote %s: %v", name, err)
		}
	}
}

func configureStarterGitFailure(t *testing.T, f onboardingFixture, state string) {
	t.Helper()
	if state == "identity" {
		t.Setenv("GIT_AUTHOR_NAME", "")
	} else {
		head, err := github.Run(f.ctx, f.repo, "git", onboardingRevParse, "HEAD")
		if err != nil {
			t.Fatal(err)
		}
		writeFixtureFile(t, filepath.Join(f.repo, ".git", "MERGE_HEAD"), head+"\n", 0600)
	}
}

func prepareLegacyOnboardingPolicy(t *testing.T, f onboardingFixture, custom bool) string {
	t.Helper()
	legacy := "# Whirlwind starter policy: ideation only.\n# Configure checks, allowed_paths and risk_rules before enabling task execution.\nchecks: []\nallowed_paths: []\ncommands: []\nrisk_rules: []\n"
	if custom {
		legacy = "# User-owned policy\nchecks: []\nallowed_paths: []\ncommands: []\nrisk_rules: []\n"
	}
	writeFixtureFile(t, filepath.Join(f.repo, onboardingPolicyYML), legacy, 0600)
	writeFixtureFile(t, filepath.Join(f.repo, "package.json"), `{"packageManager":"npm@11.6.0","scripts":{"test":"vitest run","typecheck":"tsc --noEmit","build":"next build","lint":"eslint"}}`, 0600)
	for _, args := range [][]string{{"add", onboardingPolicyYML, "package.json"}, {"commit", "-m", "Legacy starter"}} {
		if _, err := github.Run(f.ctx, f.repo, "git", args...); err != nil {
			t.Fatal(err)
		}
	}
	project := model.Project{ID: "p", Name: "Project", Repo: "whirlwind-fixture/demo", Path: f.repo}
	if err := f.server.Store.Put(f.ctx, "projects", "p", project, 0); err != nil {
		t.Fatal(err)
	}
	return legacy
}

func assertUpgradedOnboardingPolicy(t *testing.T, f onboardingFixture, body []byte) {
	t.Helper()
	var record model.Record
	if err := json.Unmarshal(body, &record); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Decode[model.Project](record)
	if err != nil || saved.PolicyHash == "" {
		t.Fatalf("policy still not ready: %s %v", body, err)
	}
	assertOnboardingChecks(t, saved.Policy.Checks)
	if _, _, err := policy.Load(f.repo); err != nil {
		t.Fatal(err)
	}
}

func assertOnboardingChecks(t *testing.T, checks []model.Check) {
	t.Helper()
	for _, name := range []string{"qlty", "test", "typecheck", "build", "lint"} {
		found := false
		for _, check := range checks {
			if check.Name == name {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing check %s: %+v", name, checks)
		}
	}
}
