package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"whirlwind/internal/control"
	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
	"whirlwind/internal/workflow"
)

// TestIsolatedWorkflowAcceptance traces one plan in a repo with no Whirlwind
// files or remote. Run from the Whirlwind root with the preloaded Go digest:
//
//	WHIRLWIND_TEST_OFFLINE_DOCKER_IMAGE=golang@sha256:87c65a688975c0b1d4585d204874c45ca78354670e3a5f11368c5a492303d696 \
//	  WHIRLWIND_TEST_SEEDED_NONO=/bin/false \
//	  go test -race ./cmd/whirlwind -run '^TestIsolatedWorkflowAcceptance$' -count=1 -v -timeout=8m
//
// The fixture owns its Git repositories, private settings, and database.
// The supervisor handles decisions, restarts, and offline checks. The fixture
// supplies model output and the Coding-to-Review handoff. If the settings-backed
// plan approval fails, the same approval logic is exercised in the stopped
// fixture's database, separately from the shipped approval result.
// WHIRLWIND_TEST_SEEDED_NONO selects an executable sentinel for the in-process
// Reviewer fixture. That phase supplies deterministic output without native ACP.
func TestIsolatedWorkflowAcceptance(t *testing.T) {
	image := os.Getenv("WHIRLWIND_TEST_OFFLINE_DOCKER_IMAGE")
	if image == "" || runtime.GOOS != "linux" {
		t.Skip("requires Linux and a preloaded immutable Go image for rootless Docker")
	}
	ctx := t.Context()
	root := t.TempDir()
	repoRoot, err := filepath.Abs("../..")
	mustSeedTerminal(t, err)
	data, home := filepath.Join(root, "state"), filepath.Join(root, "home")
	source, candidate := filepath.Join(root, "source"), filepath.Join(root, "candidate")
	for _, dir := range []string{data, home, source} {
		mustSeedTerminal(t, os.Mkdir(dir, 0700))
	}
	t.Setenv("HOME", home)
	write := func(path, content string) {
		t.Helper()
		mustSeedTerminal(t, os.WriteFile(path, []byte(content), 0600))
	}
	settings := filepath.Join(root, "settings.yaml")
	queues := map[string][]map[string]string{}
	for _, role := range model.AgentRoles() {
		queues[role] = []map[string]string{{"provider": "seeded", "model": "gpt-6.2-sol"}}
	}
	workers := map[string]any{
		"global_slots": 4, "project_slots": 2, "ideation": map[string]any{"on_registration": false, "cron": ""},
		"admission": model.AdmissionSettings{Pools: map[string]model.QuotaReserve{"seeded": {MinRemainingPercent: 10, MaxAgeSeconds: 900}}},
	}
	configuration := map[string]any{
		"version": 1, "mode": model.AgenticMode, "projects": []string{source}, "agents": queues,
		"providers": []map[string]any{{"id": "seeded", "name": "Credential-free acceptance role", "kind": "acp",
			"command": []string{filepath.Join(root, "unavailable-codex-acp")}, "capabilities": model.AgentRoles()}},
		"workers": workers,
	}
	writeSettings := func(stopped bool) {
		t.Helper()
		workers["stopped"] = stopped
		encoded, err := json.Marshal(configuration)
		mustSeedTerminal(t, err)
		write(settings, string(encoded))
	}
	writeSettings(false)
	environment := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "WHIRLWIND_ROOT=" + repoRoot,
		"WHIRLWIND_DATA_DIR=" + data, "WHIRLWIND_SETTINGS_FILE=" + settings}
	for _, key := range []string{"GOCACHE", "GOMODCACHE", "GOFLAGS"} {
		if value := os.Getenv(key); value != "" {
			environment = append(environment, key+"="+value)
		}
	}
	launcher := func(args ...string) string {
		t.Helper()
		deadline, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(deadline, filepath.Join(repoRoot, "bin", "whirlwind"), args...)
		cmd.Dir, cmd.Env = repoRoot, environment
		out, err := cmd.CombinedOutput()
		if err != nil {
			logs, _ := os.ReadFile(filepath.Join(data, "whirlwind-supervisor.log"))
			t.Fatalf("launcher %v: %v: %s\n%s", args, err, out, logs)
		}
		return string(out)
	}
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		cmd.Env = append(append([]string{}, environment...), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
			"GIT_OPTIONAL_LOCKS=0", "GIT_AUTHOR_NAME=Acceptance", "GIT_AUTHOR_EMAIL=acceptance@example.invalid",
			"GIT_COMMITTER_NAME=Acceptance", "GIT_COMMITTER_EMAIL=acceptance@example.invalid")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git(source, "init", "--template=", "-b", "acceptance-source")
	write(filepath.Join(source, "go.mod"), "module example.test/acceptance\ngo 1.23.0\n")
	write(filepath.Join(source, "result.txt"), "broken\n")
	write(filepath.Join(source, "smoke_test.go"), "package acceptance\nimport (\"os\"; \"testing\")\nfunc TestResultExists(t *testing.T) { if _, err := os.Stat(\"result.txt\"); err != nil { t.Fatal(err) } }\n")
	git(source, "add", "--all")
	git(source, "commit", "--no-gpg-sign", "-m", "Acceptance baseline")
	base := git(source, "rev-parse", "HEAD")
	sourceRefs := git(source, "show-ref")
	git(root, "clone", "--no-hardlinks", "--", source, candidate)
	git(candidate, "remote", "remove", "origin")
	git(candidate, "checkout", "--detach", base)
	t.Logf("application=%s fixture_base=%s fixture_tree=%s", git(repoRoot, "rev-parse", "HEAD"), base, git(source, "rev-parse", "HEAD^{tree}"))

	launcher("start")
	t.Cleanup(func() { launcher("stop") })
	db := openSeededTerminalStore(t, data)
	engine := workflow.New(db, nil, data)
	var projectRecord model.Record
	var project model.Project
	waitOfflineFixture(t, data, "isolated source registration", func() bool {
		records, err := db.List(ctx, "projects")
		if err != nil || len(records) != 1 {
			return false
		}
		projectRecord = records[0]
		project, err = store.Decode[model.Project](projectRecord)
		// With no committed policy, the initial setup records a project error.
		// Wait for that write before previewing the external policy version.
		return err == nil && project.Path == source && project.Status == "error"
	})
	policyPath := filepath.Join(root, "approved-checks.yaml")
	write(policyPath, "offline_docker_image: "+image+"\nallowed_paths: [result.txt, regression_test.go]\nrisk_rules: [{level: low, paths: ['**']}]\nchecks:\n  - name: regression\n    command: [go, test, -mod=readonly, -count=1, -json, './...']\n    report_format: go-test-json\n    timeout_seconds: 90\n")
	preview := launcher("policy", "preview", project.ID, policyPath)
	fields := regexp.MustCompile(`Version (\d+)\nSHA256 ([a-f0-9]{64})`).FindStringSubmatch(preview)
	if len(fields) != 3 {
		t.Fatalf("policy preview: %s", preview)
	}
	launcher("policy", "approve", project.ID, policyPath, fields[1], fields[2])
	waitOfflineFixture(t, data, "managed setup qualification", func() bool {
		projectRecord, err = db.Get(ctx, "projects", project.ID)
		if err != nil {
			return false
		}
		project, err = store.Decode[model.Project](projectRecord)
		if err == nil && project.Status == "error" && project.ManagedPolicy {
			rows, listErr := db.List(ctx, "operations")
			mustSeedTerminal(t, listErr)
			for _, row := range rows {
				op, decodeErr := store.Decode[model.Operation](row)
				if decodeErr == nil && op.Kind == "project_setup" && op.Subject == project.ID && op.Stage == "failed" && !strings.Contains(op.Error, "project policy approval required") {
					t.Fatalf("managed setup failed: %s", op.Error)
				}
			}
		}
		return err == nil && project.Status == "approved" && project.OfflineQualification != nil
	})
	if project.PolicyHash != fields[2] || project.OfflineQualification.PolicyHash != fields[2] {
		t.Fatal("setup qualification differs from the approved policy")
	}
	t.Logf("live supervisor setup project=%s policy=%s job=%s image=%s", project.ID, project.PolicyHash, project.OfflineQualification.JobID, project.OfflineQualification.ImageID)

	client := control.NewClient(control.SocketPath(data))
	call := func(request control.Request) control.Response {
		t.Helper()
		request.RequestID, request.IdempotencyKey = request.Operation, request.Operation
		response, err := client.Call(ctx, request)
		if err != nil {
			t.Fatalf("%s: %v", request.Operation, err)
		}
		return response
	}
	started := call(control.Request{Operation: "ideation.start", ProjectID: project.ID, ExpectedVersion: projectRecord.Version})
	result, ok := started.Result.(map[string]any)
	if !ok || result["operation_id"] == nil {
		t.Fatalf("expected a queued ideation identity: %+v", started)
	}
	ideationID, ok := result["operation_id"].(string)
	if !ok || ideationID == "" {
		t.Fatalf("invalid queued ideation identity: %+v", result)
	}
	var blocked model.Operation
	waitOfflineFixture(t, data, "native Ideation refusal", func() bool {
		row, readErr := db.Get(ctx, "operations", ideationID)
		blocked, err = store.Decode[model.Operation](row)
		return readErr == nil && err == nil && blocked.Stage == "failed"
	})
	if !strings.Contains(blocked.Error, "skipped (0 attempts): no available provider with required capability") {
		t.Fatalf("unexpected first Ideation blocker: %s", blocked.Error)
	}
	t.Logf("real supervisor Ideation run=%s stage=%s with credential-free fixture settings: %s", ideationID, blocked.Stage, blocked.Error)
	profile, err := execution.ForPolicy(source, true, project.PolicyHash, project.Policy.Sandbox)
	mustSeedTerminal(t, err)
	command, platformErr := execution.Command(ctx, []string{"codex-acp"}, profile)
	if command != nil || !errors.Is(platformErr, execution.ErrSandbox) || !strings.Contains(platformErr.Error(), "execution requires macOS") {
		t.Fatalf("expected the native launch gate to refuse Linux: %v", platformErr)
	}
	t.Logf("native role launch gate: %s", platformErr)

	// Preserve the failed real operation. Approve a separate seeded idea via
	// the actual socket with worker dispatch held while we publish its plan.
	launcher("stop")
	writeSettings(true)
	launcher("start")
	seededRun, ideaID := store.ID(), store.ID()
	now := time.Now().UTC()
	mustSeedTerminal(t, db.Put(ctx, "operations", seededRun, model.Operation{Kind: "ideation", Subject: project.ID,
		Stage: "complete", StartedAt: now.Format(time.RFC3339Nano), FinishedAt: now.Format(time.RFC3339Nano)}, 0))
	idea := model.Idea{ID: ideaID, ProjectID: project.ID, RunID: seededRun, Title: "Return readiness", Direction: "harden",
		What: "Correct readiness", Why: "The result currently reports broken", Where: "result.txt", HowItHelps: "Reports readiness correctly",
		NoveltyMethod: "not_checked", NoveltyOutcome: "deferred", DecisionStatus: "draft"}
	mustSeedTerminal(t, db.Put(ctx, "ideas", ideaID, idea, 0))
	rejectedIdea := idea
	rejectedIdea.ID, rejectedIdea.Title = store.ID(), "Decline unrelated change"
	mustSeedTerminal(t, db.Put(ctx, "ideas", rejectedIdea.ID, rejectedIdea, 0))
	call(control.Request{Operation: "idea.archive", ProjectID: project.ID, ResourceID: rejectedIdea.ID, ExpectedVersion: 1,
		Payload: json.RawMessage(`{"reason":"The change is outside the fixture goal."}`)})
	row, err := db.Get(ctx, "ideas", rejectedIdea.ID)
	mustSeedTerminal(t, err)
	rejectedIdea, err = store.Decode[model.Idea](row)
	mustSeedTerminal(t, err)
	if rejectedIdea.ArchivedAt == "" || rejectedIdea.PlanID != "" {
		t.Fatalf("declined idea is active or has a plan: %+v", rejectedIdea)
	}
	call(control.Request{Operation: "idea.approve", ProjectID: project.ID, ResourceID: ideaID, ExpectedVersion: 1})
	task := seedIsolatedPlanner(t, engine, project, idea, base, candidate)
	plannerID := task.LastAttemptID
	draft, err := db.Get(ctx, "execution_plans", task.ID)
	mustSeedTerminal(t, err)
	content, err := os.ReadFile(task.PlanDocument.Path)
	mustSeedTerminal(t, err)
	updated := strings.ReplaceAll(string(content), fixtureDraftTitle, "Verify revised readiness output")
	if updated == string(content) {
		t.Fatal("fixture plan edit made no change")
	}
	edit, err := json.Marshal(map[string]string{"content": updated})
	mustSeedTerminal(t, err)
	call(control.Request{Operation: "plan.document.save", ProjectID: project.ID, PlanID: task.ID, ExpectedVersion: draft.Version,
		ContentHash: task.PlanDocument.Hash, Payload: edit})
	draft, err = db.Get(ctx, "execution_plans", task.ID)
	mustSeedTerminal(t, err)
	editedTask, err := store.Decode[model.ExecutionPlan](draft)
	mustSeedTerminal(t, err)
	if editedTask.Stage != "plan_approval" || editedTask.PlanDocument.ApprovedHash != "" || editedTask.PlanDocument.Hash == task.PlanDocument.Hash {
		t.Fatal("editing the plan failed to preserve separate approval")
	}
	task = editedTask
	t.Logf("socket archived idea=%s without plan; saved revised plan=%s hash=%s while stage=%s", rejectedIdea.ID, task.ID, task.PlanDocument.Hash, task.Stage)
	call(control.Request{Operation: "plan.approve",
		ProjectID: project.ID, PlanID: task.ID, ExpectedVersion: draft.Version, ContentHash: task.PlanDocument.Hash})
	launcher("stop")
	approvedRecord, err := db.Get(ctx, "execution_plans", task.ID)
	mustSeedTerminal(t, err)
	task, err = store.Decode[model.ExecutionPlan](approvedRecord)
	mustSeedTerminal(t, err)
	if task.Stage != "coding" || task.PlanDocument.ApprovedHash != task.PlanDocument.Hash {
		t.Fatalf("unexpected plan approval stage=%s document=%+v", task.Stage, task.PlanDocument)
	}
	t.Logf("seeded Ideation run=%s idea=%s Planner=%s; idea approved by socket; plan=%s version=%d hash=%s via supervisor socket", seededRun, ideaID, plannerID, task.ID, task.PlanVersion, task.PlanDocument.ApprovedHash)

	write(filepath.Join(candidate, "regression_test.go"), "package acceptance\nimport (\"os\"; \"strings\"; \"testing\")\nfunc TestResultReady(t *testing.T) { got, err := os.ReadFile(\"result.txt\"); if err != nil || !strings.HasPrefix(string(got), \"ready\\n\") { t.Fatal(\"result is not ready\") } }\n")
	git(candidate, "add", "regression_test.go")
	git(candidate, "commit", "--no-gpg-sign", "-m", "Seed test-first red candidate")
	red := git(candidate, "rev-parse", "HEAD")
	task.Stage = "review"
	mustSeedTerminal(t, db.Put(ctx, "execution_plans", task.ID, task, approvedRecord.Version))
	writeSettings(false)
	launcher("start")
	redEvidence := awaitManagedOfflineResult(t, db, data, task.ID, red, "failed", project)
	if redEvidence.Passed || redEvidence.Results[0].Status != "fail" || !strings.Contains(redEvidence.Results[0].Output, "result is not ready") {
		t.Fatalf("expected the physical regression assertion to fail: %+v", redEvidence.Results[0])
	}
	var repaired model.ExecutionPlan
	waitOfflineFixture(t, data, "red candidate admission to repair", func() bool {
		row, readErr := db.Get(ctx, "execution_plans", task.ID)
		repaired, err = store.Decode[model.ExecutionPlan](row)
		return readErr == nil && err == nil && (repaired.Stage == "repair" || repaired.ResumeStage == "repair")
	})
	t.Logf("seeded Coding commit=%s; live red tree=%s manifest=%s passed=%t next=repair", red, redEvidence.CandidateTree, redEvidence.ManifestHash, redEvidence.Passed)
	launcher("stop")

	write(filepath.Join(candidate, "result.txt"), "ready\n")
	git(candidate, "add", "result.txt")
	git(candidate, "commit", "--no-gpg-sign", "-m", "Seed implementation repair")
	green := git(candidate, "rev-parse", "HEAD")
	repairedRecord, err := db.Get(ctx, "execution_plans", task.ID)
	mustSeedTerminal(t, err)
	repaired, err = store.Decode[model.ExecutionPlan](repairedRecord)
	mustSeedTerminal(t, err)
	if repaired.ApprovedPlanHash() != task.ApprovedPlanHash() || repaired.PlanVersion != task.PlanVersion {
		t.Fatal("restart checkpoint lost the approved plan identity")
	}
	repaired.Stage, repaired.ResumeStage, repaired.Error, repaired.VerificationID, repaired.Wait = "review", "", "", "", nil
	mustSeedTerminal(t, db.Put(ctx, "execution_plans", task.ID, repaired, repairedRecord.Version))
	launcher("start")
	greenEvidence := awaitManagedOfflineResult(t, db, data, task.ID, green, "complete", project)
	if !greenEvidence.Passed || greenEvidence.CandidateTree != git(candidate, "rev-parse", "HEAD^{tree}") {
		t.Fatalf("green report does not cover the repaired tree: %+v", greenEvidence)
	}
	var fenced model.ExecutionPlan
	waitOfflineFixture(t, data, "managed Reviewer evidence fence", func() bool {
		row, readErr := db.Get(ctx, "execution_plans", task.ID)
		fenced, err = store.Decode[model.ExecutionPlan](row)
		return readErr == nil && err == nil && fenced.Stage == "paused"
	})
	if !strings.Contains(fenced.Error, "Reviewer preparation unavailable") || fenced.ReviewID != "" || fenced.Delivery != nil {
		t.Fatalf("expected the current managed handoff fence: stage=%s error=%q review=%q delivery=%+v", fenced.Stage, fenced.Error, fenced.ReviewID, fenced.Delivery)
	}
	manifest := isolatedRunnerManifest(t, db, greenEvidence.ManifestHash)
	mustSeedTerminal(t, greenEvidence.Validate(manifest))
	staleManifest := manifest
	staleManifest.CandidateCommit, staleManifest.CandidateTree = red, redEvidence.CandidateTree
	if err := greenEvidence.Validate(staleManifest); err == nil {
		t.Fatal("green report accepted the stale red candidate identity")
	} else {
		t.Logf("retained green report refused stale candidate=%s tree=%s: %s", red, staleManifest.CandidateTree, err)
	}
	launcher("stop")
	if git(source, "rev-parse", "HEAD") != base || git(source, "show-ref") != sourceRefs || git(source, "status", "--porcelain") != "" || git(source, "remote") != "" {
		t.Fatal("acceptance altered the original source checkout")
	}
	t.Logf("seeded repair commit=%s; live green tree=%s verification=%s manifest=%s; managed review blocked: %s", green, greenEvidence.CandidateTree, manifest.JobID, greenEvidence.ManifestHash, fenced.Error)
	if nono := os.Getenv("WHIRLWIND_TEST_SEEDED_NONO"); nono != "" {
		if git(candidate, "show", green+":result.txt") != "ready" {
			t.Fatal("checks-green candidate did not omit the separately approved review marker")
		}
		t.Log("checks-green candidate lacks the approved review marker; continuing with deterministic Reviewer output")
		exerciseSeededOfflineHandoffs(t, db, data, project, task.ID, candidate, green, nono, git)
		if git(candidate, "show", "HEAD:result.txt") != "ready\nreview-correction" {
			t.Fatal("accepted repair did not restore the approved review marker")
		}
		assertIsolatedReviewIdentity(t, openSeededTerminalStore(t, data), task.ID)
	}
}

func isolatedRunnerManifest(t *testing.T, db *store.Store, digest string) verification.JobManifest {
	t.Helper()
	rows, err := db.List(t.Context(), "verification")
	mustSeedTerminal(t, err)
	for _, row := range rows {
		saved, err := store.Decode[struct {
			Manifest verification.JobManifest `json:"runner_manifest"`
		}](row)
		mustSeedTerminal(t, err)
		if saved.Manifest.Digest() == digest {
			return saved.Manifest
		}
	}
	t.Fatalf("retained verification manifest %s not found", digest)
	return verification.JobManifest{}
}

func assertIsolatedReviewIdentity(t *testing.T, db *store.Store, id string) {
	t.Helper()
	ctx := t.Context()
	row, err := db.Get(ctx, "execution_plans", id)
	mustSeedTerminal(t, err)
	task, err := store.Decode[model.ExecutionPlan](row)
	mustSeedTerminal(t, err)
	row, err = db.Get(ctx, "reviews", task.ReviewID)
	mustSeedTerminal(t, err)
	review, err := store.Decode[model.Review](row)
	mustSeedTerminal(t, err)
	if review.Adherence == nil || !review.Adherence.Accepted {
		t.Fatal("seeded repair did not have a passing requirement verdict")
	}
	proof := review.Adherence.Provenance
	newPlan := proof
	newPlan.PlanVersion++
	if _, err := workflow.EvaluateAdherence(task.Plan.Requirements, review.Requirements, newPlan, proof, true); err == nil {
		t.Fatal("prior requirement verdict was accepted for a later plan version")
	} else {
		t.Logf("requirement verdict %s for candidate=%s plan_version=%d refused for plan_version=%d: %s", task.ReviewID, proof.Head, proof.PlanVersion, newPlan.PlanVersion, err)
	}
}

func seedIsolatedPlanner(t *testing.T, engine *workflow.Engine, project model.Project, idea model.Idea, base, candidate string) model.ExecutionPlan {
	t.Helper()
	ctx, db := t.Context(), engine.Store
	now := time.Now().UTC()
	specification := seededTerminalSpecification()
	specification.Where = []string{"result.txt", "regression_test.go"}
	specification.Units[0].AllowedPaths = specification.Where
	specification.Requirements[0].Outcome = "result.txt contains ready followed by the review-correction marker on the next line."
	specification.Requirements[0].EvidenceMethod = "Inspect both result.txt lines. TestResultReady covers the readiness line."
	specification.Checks = []model.Check{{Name: "regression", Command: project.Policy.Checks[0].Command}}
	task := model.ExecutionPlan{ID: model.PlanID("idea:" + idea.ID), IdeaID: idea.ID, IdeationRunID: idea.RunID, ProjectID: project.ID,
		Mode: model.AgenticMode, PlanVersion: 1, Stage: "planning", Base: base, Worktree: candidate, Issue: model.Issue{Title: idea.Title}, Plan: specification,
		Selection: &model.SelectionAuthorization{CandidateID: idea.ID, SelectedAt: now.Format(time.RFC3339Nano), PolicyHash: project.PolicyHash}}
	mustSeedTerminal(t, db.Put(ctx, "execution_plans", task.ID, task, 0))
	planner := model.Attempt{AttemptID: store.ID(), Task: task.ID, ProjectID: project.ID, IdeaID: idea.ID, OperationID: idea.RunID,
		Role: "plan", PlanVersion: 1, Status: "running", StartedAt: now.Format(time.RFC3339Nano), DeadlineAt: now.Add(time.Minute).Format(time.RFC3339Nano)}
	mustSeedTerminal(t, db.Put(ctx, "sessions", planner.AttemptID, planner, 0))
	task.LastAttemptID = planner.AttemptID
	mustSeedTerminal(t, engine.WriteLocalPlan(ctx, project, &task))
	mustSeedTerminal(t, db.PlannerRunLocation(ctx, planner.AttemptID, task.PlanDocument.Path))
	planner.Status, planner.Finished, planner.FinishedAt = model.AttemptAwaitingPlanPublication, true, time.Now().UTC().Format(time.RFC3339Nano)
	mustSeedTerminal(t, db.Put(ctx, "sessions", planner.AttemptID, planner, 1))
	mustSeedTerminal(t, db.CommitPlannerPublication(ctx, task, 1, func() error { return nil }))
	return task
}
