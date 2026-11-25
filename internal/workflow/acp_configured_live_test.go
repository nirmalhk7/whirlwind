package workflow

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

type liveACPSettings struct {
	Providers []model.Provider               `json:"providers"`
	Agents    map[string][]model.AgentChoice `json:"agents"`
}

// Opting in spends normal account usage. An enabled but unqualified stage fails;
// metadata, absent configuration, and skipped prerequisites never certify inference.
func TestConfiguredACPLiveAcceptance(t *testing.T) {
	if os.Getenv("WHIRLWIND_TEST_ACP_CONFIGURED_LIVE") != "1" {
		t.Skip("set WHIRLWIND_TEST_ACP_CONFIGURED_LIVE=1 and WHIRLWIND_LIVE_SETTINGS to test configured ACP providers")
	}
	settings := readLiveACPSettings(t)
	t.Setenv("WHIRLWIND_DATA_DIR", t.TempDir())
	t.Logf("platform=%s/%s Go=%s", runtime.GOOS, runtime.GOARCH, runtime.Version())
	logLiveVersion(t, "nono")
	logLiveVersion(t, "codex")
	logLiveVersion(t, "claude")
	for _, id := range []string{"claude", "codex", "cursor"} {
		t.Run(id, func(t *testing.T) { qualifyLiveACP(t, settings, id) })
	}
	t.Run("configured-fallback", func(t *testing.T) { qualifyLiveFallback(t, settings) })
}

func readLiveACPSettings(t *testing.T) liveACPSettings {
	t.Helper()
	path := os.Getenv("WHIRLWIND_LIVE_SETTINGS")
	if path == "" {
		t.Fatal("WHIRLWIND_LIVE_SETTINGS must name the authorized settings.yaml")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Match the JSON field names used by stored provider and agent choices, including config_id.
	var fields map[string]any
	if err = yaml.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	var settings liveACPSettings
	if err = json.Unmarshal(encoded, &settings); err != nil {
		t.Fatal(err)
	}
	t.Logf("settings=%s sha256=%x", path, sha256.Sum256(raw))
	return settings
}

func liveRole() string {
	if role := os.Getenv("WHIRLWIND_LIVE_ROLE"); role != "" {
		return role
	}
	return "plan"
}

func liveConfiguredProvider(settings liveACPSettings, id string) (model.Provider, error) {
	for _, p := range settings.Providers {
		if p.ID == id {
			return p, nil
		}
	}
	return model.Provider{}, fmt.Errorf("provider %s is absent from configured settings", id)
}

func liveConfiguredChoice(settings liveACPSettings, id string) (model.AgentChoice, error) {
	for _, choice := range settings.Agents[liveRole()] {
		if choice.Provider == id && choice.Model != "" {
			return choice, nil
		}
	}
	return model.AgentChoice{}, fmt.Errorf("no explicit %s model/thinking choice for provider %s; configure that role before inference qualification", liveRole(), id)
}

func qualifyLiveACP(t *testing.T, settings liveACPSettings, id string) {
	t.Helper()
	p, err := liveConfiguredProvider(settings, id)
	if err != nil {
		t.Fatal("incomplete configuration: ", err)
	}
	logLiveAdapterVersion(t, p.Command)
	resolved, transportErr := liveACPTransport(p, id)
	if transportErr != nil {
		t.Errorf("incomplete configuration: %v", transportErr)
		for _, stage := range []string{"metadata", "inference", "effective-settings", "tools", "resume", "unsupported-settings", "missing-authentication"} {
			t.Logf("%s=incomplete (configured transport/authentication profile unavailable)", stage)
		}
		return
	}
	p = resolved
	logLiveCodexBackend(t, p)
	choice, choiceErr := liveConfiguredChoice(settings, id)
	if liveRole() == "code" {
		if choiceErr != nil {
			t.Fatalf("incomplete Coding selection: %v", choiceErr)
		}
		t.Run("writable-candidate", func(t *testing.T) { qualifyLiveCodingCandidate(t, p, choice) })
		return
	}
	cwd := t.TempDir()
	t.Run("metadata", func(t *testing.T) { qualifyLiveCatalog(t, p, cwd) })
	t.Run("missing-authentication", func(t *testing.T) { qualifyMissingLiveAuth(t, p) })
	if choiceErr != nil {
		t.Errorf("incomplete selection: %v", choiceErr)
		t.Log("inference/effective-settings/tools/resume/unsupported-settings=incomplete (no configured selection)")
		return
	}
	qualifyLiveReadOnlyTurns(t, p, cwd, choice)
}

func qualifyLiveReadOnlyTurns(t *testing.T, p model.Provider, cwd string, choice model.AgentChoice) {
	t.Helper()
	t.Logf("configured role=%s model=%s thinking=%+v; sandbox grants unchanged", liveRole(), choice.Model, choice.Thinking)
	input := provider.Input{Role: liveRole(), CWD: cwd, ReadOnly: true, Model: choice.Model, Thinking: choice.Thinking, Policy: model.Policy{Sandbox: p.Sandbox}}
	adapter := provider.ProcessAdapter{Provider: p}
	t.Run("unsupported-model", func(t *testing.T) {
		bad := input
		bad.Model = "whirlwind-unsupported-" + liveNonce(t)
		assertUnsupportedLiveSetting(t, adapter, bad, "model")
	})
	t.Run("unsupported-thinking", func(t *testing.T) {
		bad := input
		bad.Thinking = &model.ThinkingChoice{ConfigID: "whirlwind-unsupported", Value: "whirlwind-unsupported"}
		assertUnsupportedLiveSetting(t, adapter, bad, "thinking")
	})
	// The supervisor reuses private runtime state across compatible turns. A
	// fresh standalone sandbox cannot load the first turn's saved session.
	profile, err := execution.ForProvider(input.CWD, input.ReadOnly, "", p.Sandbox, p.Command)
	requireWorkflowSuccess(t, err)
	profile.RuntimeDir = t.TempDir()
	input.Execution = &profile
	marker := "WHIRLWIND_" + liveNonce(t)
	input.Prompt = "Do not use tools or inspect files. Remember this marker for our next turn and reply with only it: " + marker
	var first provider.Output
	ready := t.Run("inference-and-effective-settings", func(t *testing.T) {
		reply := &liveReplyEvidence{}
		first = runLiveACP(t, adapter, input, provider.Hooks{Event: reply.event})
		assertLiveChoice(t, first, choice)
		t.Logf("confirmed model=%s thinking=%+v session established=%t reply bytes=%d", first.ResolvedModel, first.EffectiveThinking, first.SessionID != "", len(first.Text))
		reply.assertStream(t)
		if strings.TrimSpace(first.Text) != marker || first.SessionID == "" {
			t.Fatal("inference did not return exact marker and a session ID")
		}
		t.Log("authenticated inference and confirmed model/thinking passed")
	})
	if !ready || first.SessionID == "" {
		t.Log("tools=incomplete; resume=incomplete (configured inference did not succeed)")
		return
	}
	t.Run("resume", func(t *testing.T) {
		resumed := input
		resumed.SessionID = first.SessionID
		resumed.Prompt = "Do not use tools or inspect files. Reply with only the marker from our previous turn."
		reply := &liveReplyEvidence{}
		out := runLiveACP(t, adapter, resumed, provider.Hooks{Event: reply.event})
		assertLiveChoice(t, out, choice)
		reply.assertStream(t)
		if out.SessionID != first.SessionID || strings.TrimSpace(out.Text) != marker {
			t.Fatal("resumed session did not retain identity and previous-turn context")
		}
	})
	t.Run("tools", func(t *testing.T) { qualifyLiveReadTool(t, adapter, input, choice) })
}

func qualifyLiveCatalog(t *testing.T, p model.Provider, cwd string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	catalog, err := provider.ModelsWithSupervisorDir(ctx, p, cwd, t.TempDir())
	if err != nil {
		t.Fatalf("metadata incomplete; inference not certified: %v", err)
	}
	if len(catalog.Models) == 0 {
		t.Fatal("metadata incomplete: adapter advertised no models")
	}
	t.Logf("metadata only: models=%d current=%q", len(catalog.Models), catalog.Current)
}

func liveACPTransport(p model.Provider, id string) (model.Provider, error) {
	resolved, transportErr := provider.ResolveExecutionProvider(p)
	if transportErr == nil && resolved.ACPProfile != id {
		transportErr = fmt.Errorf("provider %s must explicitly select its bundled ACP authentication profile; current acp_profile=%q", id, resolved.ACPProfile)
	}
	return resolved, transportErr
}

type liveReplyEvidence struct {
	mu      sync.Mutex
	deltas  int
	stopped bool
}

func (e *liveReplyEvidence) event(kind string, payload any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if kind == "whirlwind/stop" {
		e.stopped = true
	}
	if kind == "item/agentMessage/delta" {
		e.deltas++
	}
	if kind != "session/update" {
		return
	}
	raw, _ := json.Marshal(payload)
	var event struct {
		Params struct {
			Update struct {
				Kind string `json:"sessionUpdate"`
			} `json:"update"`
		} `json:"params"`
	}
	if json.Unmarshal(raw, &event) == nil && event.Params.Update.Kind == "agent_message_chunk" {
		e.deltas++
	}
}
func (e *liveReplyEvidence) assertStream(t *testing.T) {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	t.Logf("streamed reply chunks=%d completion=%t", e.deltas, e.stopped)
	if e.deltas == 0 || !e.stopped {
		t.Fatal("streamed inference/completion incomplete")
	}
}

func liveNonce(t *testing.T) string {
	t.Helper()
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(value[:])
}
func runLiveACP(t *testing.T, adapter provider.ProcessAdapter, input provider.Input, hooks provider.Hooks) provider.Output {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	out, err := adapter.Run(ctx, input, hooks)
	if err != nil {
		t.Logf("session established=%t; confirmed model=%q", out.SessionID != "", out.ResolvedModel)
		t.Fatalf("configured ACP stage incomplete: %v", err)
	}
	return out
}
func assertLiveChoice(t *testing.T, out provider.Output, choice model.AgentChoice) {
	t.Helper()
	if out.ResolvedModel != choice.Model {
		t.Fatalf("unconfirmed effective model: got %q want %q", out.ResolvedModel, choice.Model)
	}
	if choice.Thinking != nil && (out.EffectiveThinking == nil || *out.EffectiveThinking != *choice.Thinking) {
		t.Fatalf("unconfirmed effective thinking: got %+v want %+v", out.EffectiveThinking, choice.Thinking)
	}
}
func assertUnsupportedLiveSetting(t *testing.T, adapter provider.ProcessAdapter, input provider.Input, setting string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	err := adapter.CheckReadiness(ctx, input)
	confirmedRejection := err != nil && strings.Contains(err.Error(), "ACP model selection unavailable")
	if setting == "thinking" {
		confirmedRejection = err != nil && strings.Contains(err.Error(), "ACP thinking level") && strings.Contains(err.Error(), "not advertised for selected model")
	}
	if !confirmedRejection {
		t.Fatalf("unsupported %s validation incomplete (transport/auth errors do not qualify): %v", setting, err)
	}
	t.Logf("unsupported %s rejected: %v", setting, err)
}
func qualifyMissingLiveAuth(t *testing.T, p model.Provider) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	profile, _ := provider.ACPProfileByID(p.ACPProfile)
	for _, name := range profile.AuthReferences {
		if name == strings.ToUpper(name) {
			t.Setenv(name, "")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := provider.CheckConnectionWithSupervisorDir(ctx, p, t.TempDir(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "no authorized credential") || !strings.Contains(err.Error(), "authenticate") {
		t.Fatalf("missing-authentication rejection incomplete: %v", err)
	}
	t.Log("missing authorized authentication rejected with setup guidance")
}

func qualifyLiveReadTool(t *testing.T, adapter provider.ProcessAdapter, input provider.Input, choice model.AgentChoice) {
	t.Helper()
	marker := "TOOL_" + liveNonce(t)
	path := filepath.Join(input.CWD, "acceptance-probe.txt")
	if err := os.WriteFile(path, []byte(marker), 0600); err != nil {
		t.Fatal(err)
	}
	evidence := &liveReadEvidence{reads: map[string]bool{}}
	hooks := provider.Hooks{Permission: func(context.Context, provider.Permission) (bool, error) { return false, nil }, Event: evidence.event}
	input.SessionID = ""
	input.Prompt = "Read only acceptance-probe.txt in the working directory using a file-read tool. Reply with only its exact contents. Do not inspect other files, write files, or use network tools."
	out := runLiveACP(t, adapter, input, hooks)
	assertLiveChoice(t, out, choice)
	evidence.mu.Lock()
	observed := evidence.observed
	evidence.mu.Unlock()
	if !observed || strings.TrimSpace(out.Text) != marker {
		t.Fatal("successful tool execution unverified: require read event and exact unpredictable file contents")
	}
	t.Log("read tool returned the controlled file contents under unchanged read-only policy")
}

type liveReadEvidence struct {
	mu       sync.Mutex
	reads    map[string]bool
	observed bool
}

func (e *liveReadEvidence) event(kind string, payload any) {

	if kind == "fs/read_text_file" {
		e.mu.Lock()
		e.observed = true
		e.mu.Unlock()
	}
	if kind != "session/update" {
		return
	}
	raw, _ := json.Marshal(payload)
	var event struct {
		Params struct {
			Update struct {
				Kind   string `json:"kind"`
				CallID string `json:"toolCallId"`
				Status string `json:"status"`
			} `json:"update"`
		} `json:"params"`
	}
	if json.Unmarshal(raw, &event) == nil {
		e.mu.Lock()
		update := event.Params.Update
		if update.CallID != "" && update.Kind == "read" {
			e.reads[update.CallID] = true
		}
		if update.Status == "completed" && (update.Kind == "read" || e.reads[update.CallID]) {
			e.observed = true
		}
		e.mu.Unlock()
	}
}

func logLiveVersion(t *testing.T, command string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	path, err := exec.LookPath(command)
	if err != nil {
		t.Logf("runtime %s unavailable", command)
		return
	}
	raw, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		t.Logf("runtime %s version unavailable: %v", path, err)
		return
	}
	t.Logf("runtime %s: %s", path, strings.TrimSpace(string(raw)))
}

// The ACP package may bundle a different Codex binary than the host PATH CLI.
func logLiveCodexBackend(t *testing.T, p model.Provider) {
	t.Helper()
	if p.ACPProfile != "codex" {
		return
	}
	if p.Sandbox != nil && slices.Contains(p.Sandbox.Environment, "CODEX_PATH") && os.Getenv("CODEX_PATH") != "" {
		logLiveVersion(t, os.Getenv("CODEX_PATH"))
		return
	}
	for _, arg := range p.Command[1:] {
		if !filepath.IsAbs(arg) || !strings.HasSuffix(arg, ".js") {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		script := `process.stdout.write(require('module').createRequire(process.argv[1]).resolve('@openai/codex/bin/codex.js'))`
		raw, err := exec.CommandContext(ctx, p.Command[0], "-e", script, arg).Output()
		cancel()
		if err != nil {
			t.Logf("bundled Codex version resolution incomplete: %v", err)
			return
		}
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		version, err := exec.CommandContext(ctx, p.Command[0], string(raw), "--version").Output()
		cancel()
		if err != nil {
			t.Logf("bundled Codex version incomplete: %v", err)
			return
		}
		t.Logf("ACP backend %s: %s", raw, strings.TrimSpace(string(version)))
		return
	}
	t.Log("ACP backend version incomplete: custom Codex command has no resolvable bundled runtime")
}

func logLiveAdapterVersion(t *testing.T, command []string) {
	t.Helper()
	if len(command) == 0 {
		t.Log("adapter command unavailable")
		return
	}
	logLiveVersion(t, command[0])
	for _, arg := range command[1:] {
		if !filepath.IsAbs(arg) || !strings.HasSuffix(arg, ".js") {
			continue
		}
		for dir := filepath.Dir(arg); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
			raw, err := os.ReadFile(filepath.Join(dir, "package.json"))
			if err != nil {
				continue
			}
			var pkg struct{ Name, Version string }
			if json.Unmarshal(raw, &pkg) == nil && pkg.Name != "" {
				t.Logf("adapter %s: %s@%s", arg, pkg.Name, pkg.Version)
				break
			}
		}
	}
}

// Fault only the configured first choice; fallback execution uses the real adapter,
// unchanged remaining choices, a fresh session, and the same read-only workspace.
func qualifyLiveFallback(t *testing.T, settings liveACPSettings) {
	t.Helper()
	chain := settings.Agents[liveRole()]
	if len(chain) < 2 {
		t.Fatal("fallback=incomplete: selected role has no configured fallback; no entries or permissions were invented")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	engine, project := liveFallbackEngine(t, ctx, settings, chain)
	recorder := &liveFallbackRecorder{primary: chain[0], cwd: project.Path}
	engine.Adapter = recorder.adapter
	task := model.ExecutionPlan{ID: "live-fallback-plan", ProjectID: project.ID}
	role, marker := liveRole(), "FALLBACK_"+liveNonce(t)
	prompt := "Reply with only " + marker + ". Do not use tools or inspect or modify files."
	if role == "plan" {
		task.Stage, task.PlanVersion = "planning", 1
		if err := engine.Store.Put(ctx, "execution_plans", task.ID, task, 0); err != nil {
			t.Fatal(err)
		}
		fixture := model.PlanSpecification{
			Objective: marker, What: "Run a fixture check.", Why: "Verify fallback.", Benefit: "Confirm live inference.",
			Where: []string{"fixture.txt"}, Scope: []string{"Read-only qualification."},
			Exclusions: []string{"No project changes."}, Steps: []string{"Run the fixture check."},
			RepositoryTestInventory:  "None configured: empty fixture repository.",
			RepositoryBuildInventory: "None configured: empty fixture repository.",
			AcceptanceCriteria:       []string{"The fixture check exits zero."},
			Requirements:             []model.Requirement{{ID: "FALLBACK-1", Outcome: "The fixture check exits zero.", EvidenceMethod: "Run the fixture check."}},
			Checks:                   []model.Check{{Name: "fixture", Command: []string{"/usr/bin/true"}}},
		}
		prompt = "Return only this JSON plan exactly. Do not use tools or inspect or modify files.\n" + jsonText(fixture)
	}
	out, err := engine.agent(ctx, project, task, provider.Input{Role: role, CWD: project.Path, ReadOnly: true, Prompt: prompt})
	matched := strings.TrimSpace(out.Text) == marker
	if role == "plan" {
		spec, decodeErr := decodePlan(out.Text)
		matched = decodeErr == nil && spec.Objective == marker
	}
	if err != nil || len(recorder.calls) < 2 || !matched {
		t.Fatalf("configured fallback incomplete: calls=%v err=%v", recorder.calls, err)
	}
	for _, choice := range chain[1:] {
		last := recorder.calls[len(recorder.calls)-1]
		if choice.Provider == last.Provider && choice.Model == last.Model && reflect.DeepEqual(choice.Thinking, last.Thinking) {
			assertLiveChoice(t, out, choice)
			t.Log("configured fallback passed after injected primary quota failure; inference was real")
			return
		}
	}
	t.Fatal("fallback used an unconfigured choice")
}

type liveFallbackRecorder struct {
	calls   []model.AgentChoice
	primary model.AgentChoice
	cwd     string
}

func (r *liveFallbackRecorder) adapter(p model.Provider) provider.Adapter {
	return liveFallbackAdapter(func(ctx context.Context, in provider.Input, hooks provider.Hooks) (provider.Output, error) {
		r.calls = append(r.calls, model.AgentChoice{Provider: p.ID, Model: in.Model, Thinking: in.Thinking})
		if len(r.calls) == 1 {
			if p.ID != r.primary.Provider || in.Model != r.primary.Model {
				return provider.Output{}, errors.New("configured primary could not execute; fallback fault was not injected")
			}
			return provider.Output{SessionID: "injected-failed-session"}, errors.New("usageLimitExceeded")
		}
		if in.SessionID != "" || in.CWD != r.cwd {
			return provider.Output{}, errors.New("fallback reused failed session or changed workspace")
		}
		return (provider.ProcessAdapter{Provider: p}).Run(ctx, in, hooks)
	})
}

func liveFallbackEngine(t *testing.T, ctx context.Context, settings liveACPSettings, chain []model.AgentChoice) (*Engine, model.Project) {
	t.Helper()
	state, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := state.Close(); err != nil {
			t.Error(err)
		}
	})
	engine := New(state, nil, t.TempDir())
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"-c", "user.name=Whirlwind Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "--quiet", "-m", "ACP fallback fixture"},
	} {
		command := exec.CommandContext(ctx, "git", args...)
		command.Dir = repo
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("prepare fallback repository: %v: %s", err, output)
		}
	}
	project := model.Project{ID: "live-fallback", Name: "ACP fallback fixture", Path: repo}
	for _, p := range settings.Providers {
		p.Available = provider.Available(p)
		p.Quota = "unknown"
		if err := state.Put(ctx, "providers", p.ID, p, 0); err != nil {
			t.Fatal(err)
		}
		project.Policy.Providers = append(project.Policy.Providers, p.ID)
	}
	if err := state.Put(ctx, "projects", project.ID, project, 0); err != nil {
		t.Fatal(err)
	}
	if err := state.Put(ctx, "agents", liveRole(), model.AgentConfig{Role: liveRole(), Chain: chain}, 0); err != nil {
		t.Fatal(err)
	}
	return engine, project
}

type liveFallbackAdapter func(context.Context, provider.Input, provider.Hooks) (provider.Output, error)

func (f liveFallbackAdapter) Run(ctx context.Context, in provider.Input, hooks provider.Hooks) (provider.Output, error) {
	return f(ctx, in, hooks)
}
