package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
	"whirlwind/internal/workflow"
)

// The launcher and verifier subprocess are real. The plan and candidate commits
// stand in for role output, and no provider credential is configured.
func TestManagedOfflineVerificationThroughLauncher(t *testing.T) {
	image := os.Getenv("WHIRLWIND_TEST_OFFLINE_DOCKER_IMAGE")
	if image == "" {
		t.Skip("set a preloaded immutable Go image for physical managed verification")
	}
	root := t.TempDir()
	repoRoot, err := filepath.Abs("../..")
	mustSeedTerminal(t, err)
	data, home, source, candidate := filepath.Join(root, "state"), filepath.Join(root, "home"), filepath.Join(root, "project"), filepath.Join(root, "candidate")
	for _, dir := range []string{data, home, source} {
		mustSeedTerminal(t, os.Mkdir(dir, 0700))
	}
	ctx := t.Context()
	t.Setenv("HOME", home)
	settings := filepath.Join(root, "settings.yaml")
	fixtureCommand := filepath.Join(root, "unavailable-seeded-provider")
	queues := map[string][]map[string]string{}
	for _, role := range model.AgentRoles() {
		queues[role] = []map[string]string{{"provider": "seeded", "model": "seeded-role-output"}}
	}
	raw, err := json.Marshal(map[string]any{"version": 1, "mode": model.AgenticMode, "projects": []string{source}, "agents": queues,
		"providers": []map[string]any{{"id": "seeded", "name": "Seeded test role", "kind": "acp", "command": []string{fixtureCommand}, "capabilities": model.AgentRoles()}},
		"workers": map[string]any{"global_slots": 4, "project_slots": 2, "ideation": map[string]any{"on_registration": false, "cron": ""},
			"admission": model.AdmissionSettings{Pools: map[string]model.QuotaReserve{"seeded": {MinRemainingPercent: 10, MaxAgeSeconds: 900}}}}})
	mustSeedTerminal(t, err)
	mustSeedTerminal(t, os.WriteFile(settings, raw, 0600))
	launcher := func(args ...string) string {
		t.Helper()
		c, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		cmd := exec.CommandContext(c, filepath.Join(repoRoot, "bin", "whirlwind"), args...)
		cmd.Dir = repoRoot
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "WHIRLWIND_ROOT=" + repoRoot, "WHIRLWIND_DATA_DIR=" + data, "WHIRLWIND_SETTINGS_FILE=" + settings}
		for _, key := range []string{"GOCACHE", "GOMODCACHE", "GOFLAGS"} {
			if value := os.Getenv(key); value != "" {
				cmd.Env = append(cmd.Env, key+"="+value)
			}
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			logs, _ := os.ReadFile(filepath.Join(data, "whirlwind-supervisor.log"))
			t.Fatalf("launcher %v: %v: %s\n%s", args, err, out, logs)
		}
		return string(out)
	}
	t.Cleanup(func() { launcher("stop") })

	git := func(repo string, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo, "-c", "user.name=Offline Fixture", "-c", "user.email=offline@example.invalid"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_OPTIONAL_LOCKS=0")
		data, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture Git %v: %v: %s", args, err, data)
		}
		return strings.TrimSpace(string(data))
	}
	write := func(path, body string) { t.Helper(); mustSeedTerminal(t, os.WriteFile(path, []byte(body), 0600)) }
	git(source, "init", "--template=", "-b", "fixture-source")
	write(filepath.Join(source, "go.mod"), "module example.test/managed-offline\ngo 1.23.0\n")
	write(filepath.Join(source, "result.txt"), "ready\n")
	write(filepath.Join(source, "smoke_test.go"), "package result\nimport (\"os\"; \"testing\")\nfunc TestDataPresent(t *testing.T) { if _, err := os.ReadFile(\"result.txt\"); err != nil { t.Fatal(err) } }\n")
	git(source, "add", "--all")
	git(source, "commit", "--no-gpg-sign", "-m", "Fixture baseline")
	base := git(source, "rev-parse", "HEAD")
	signature := func() string {
		t.Helper()
		index, err := os.ReadFile(filepath.Join(source, ".git", "index"))
		mustSeedTerminal(t, err)
		config, err := os.ReadFile(filepath.Join(source, ".git", "config"))
		mustSeedTerminal(t, err)
		return model.Fingerprint(index) + model.Fingerprint(config) + git(source, "status", "--porcelain") + git(source, "for-each-ref", "--format=%(refname) %(objectname)")
	}
	initial := signature()
	git(root, "clone", "--no-hardlinks", "--", source, candidate)
	git(candidate, "checkout", "--detach", base)

	launcher("start")
	db := openSeededTerminalStore(t, data)
	engine := workflow.New(db, nil, data)
	var project model.Project
	waitOfflineFixture(t, data, "project registration", func() bool {
		records, err := db.List(ctx, "projects")
		if err != nil || len(records) != 1 {
			return false
		}
		project, err = store.Decode[model.Project](records[0])
		return err == nil
	})
	policyPath := filepath.Join(root, "reviewed-policy.yaml")
	write(policyPath, "offline_docker_image: "+image+"\nallowed_paths: [result.txt, regression_test.go]\nrisk_rules: [{level: low, paths: ['**']}]\nchecks:\n  - name: regression\n    command: [go, test, -mod=readonly, -count=1, -json, './...']\n    report_format: go-test-json\n    timeout_seconds: 90\n")
	preview := launcher("policy", "preview", project.ID, policyPath)
	fields := regexp.MustCompile(`Version (\d+)\nSHA256 ([a-f0-9]{64})`).FindStringSubmatch(preview)
	if len(fields) != 3 {
		t.Fatalf("read policy preview: %s", preview)
	}
	launcher("policy", "approve", project.ID, policyPath, fields[1], fields[2])
	waitOfflineFixture(t, data, "managed offline setup", func() bool {
		current, err := engine.Project(ctx, project.ID)
		if err != nil {
			return false
		}
		project = current
		if project.Status == "error" && project.ManagedPolicy {
			rows, _ := db.List(ctx, "operations")
			for _, row := range rows {
				op, _ := store.Decode[model.Operation](row)
				if op.Kind == "project_setup" && op.Error != "" && op.ConfigurationHash == project.PolicyHash {
					t.Fatalf("managed setup failed: %s", op.Error)
				}
			}
		}
		return project.Status == "approved" && project.OfflineQualification != nil
	})
	if project.PolicyHash != fields[2] || project.OfflineQualification.PolicyHash != fields[2] {
		t.Fatalf("setup qualification differs from the approved policy: %+v", project.OfflineQualification)
	}
	t.Logf("qualified project=%s policy=%s setup=%s image=%s", project.ID, project.PolicyHash, project.OfflineQualification.JobID, project.OfflineQualification.ImageID)

	launcher("stop")
	write(filepath.Join(candidate, "regression_test.go"), "package result\nimport (\"os\"; \"strings\"; \"testing\")\nfunc TestResultReady(t *testing.T) { data, err := os.ReadFile(\"result.txt\"); if err != nil || !strings.HasPrefix(string(data), \"ready\\n\") { t.Fatal(\"result is not ready\") } }\n")
	write(filepath.Join(candidate, "result.txt"), "broken\n")
	git(candidate, "add", "--all")
	git(candidate, "commit", "--no-gpg-sign", "-m", "Seed red candidate")
	red := git(candidate, "rev-parse", "HEAD")
	task := seedOfflineCandidatePlan(t, engine, db, project, candidate, base)
	launcher("start")
	redEvidence := awaitManagedOfflineResult(t, db, data, task.ID, red, "failed", project)
	if redEvidence.Results[0].Status != "fail" || !strings.Contains(redEvidence.Results[0].Output, "TestResultReady") {
		t.Fatalf("expected an actual failing assertion: %+v", redEvidence.Results[0])
	}
	waitOfflineFixture(t, data, "repair transition", func() bool {
		record, _ := db.Get(ctx, "execution_plans", task.ID)
		current, _ := store.Decode[model.ExecutionPlan](record)
		return current.Stage == "repair" || current.ResumeStage == "repair"
	})
	launcher("stop")

	// A disposable, manually prepared repair replaces the agent output while
	// the supervisor is stopped. Production dispatch owns the next verification.
	write(filepath.Join(candidate, "result.txt"), "ready\nrepaired\n")
	git(candidate, "add", "result.txt")
	git(candidate, "commit", "--no-gpg-sign", "-m", "Seed repair")
	green := git(candidate, "rev-parse", "HEAD")
	record, err := db.Get(ctx, "execution_plans", task.ID)
	mustSeedTerminal(t, err)
	current, err := store.Decode[model.ExecutionPlan](record)
	mustSeedTerminal(t, err)
	current.Stage, current.ResumeStage, current.Error, current.VerificationID, current.Wait = "review", "", "", "", nil
	mustSeedTerminal(t, db.Put(ctx, "execution_plans", current.ID, current, record.Version))
	launcher("start")
	greenEvidence := awaitManagedOfflineResult(t, db, data, task.ID, green, "complete", project)
	if !greenEvidence.Passed {
		t.Fatal("green report did not pass")
	}
	waitOfflineFixture(t, data, "managed Reviewer evidence boundary", func() bool {
		record, _ := db.Get(ctx, "execution_plans", task.ID)
		current, _ := store.Decode[model.ExecutionPlan](record)
		return current.Stage == "paused" && strings.Contains(current.Error, "Reviewer preparation unavailable")
	})
	launcher("stop")
	if nono := os.Getenv("WHIRLWIND_TEST_SEEDED_NONO"); nono != "" {
		exerciseSeededOfflineHandoffs(t, db, data, project, task.ID, candidate, green, nono, git)
	}
	if signature() != initial || git(source, "rev-parse", "HEAD") != base {
		t.Fatal("supervisor changed the original checkout")
	}
	t.Logf("seeded red=%s repair=%s qualified as green=%s; shipped supervisor reached the Reviewer preparation gate", red, green, green)
}

func seedOfflineCandidatePlan(t *testing.T, e *workflow.Engine, db *store.Store, project model.Project, candidate, base string) model.ExecutionPlan {
	t.Helper()
	ctx := t.Context()
	seed := func(step string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed %s: %v", step, err)
		}
	}
	const ideaID, ideationID = "fixture-idea", "fixture-ideation"
	task := model.ExecutionPlan{ID: model.PlanID("idea:" + ideaID), IdeaID: ideaID, IdeationRunID: ideationID, ProjectID: project.ID, Mode: model.AgenticMode, PlanVersion: 1, Base: base, Worktree: candidate,
		Issue: model.Issue{Title: "Repair the result"}, Selection: &model.SelectionAuthorization{CandidateID: ideaID, SelectedAt: time.Now().UTC().Format(time.RFC3339Nano), PolicyHash: project.PolicyHash}, Plan: seededTerminalSpecification()}
	task.Plan.Where = []string{"result.txt", "regression_test.go"}
	task.Plan.Units[0].AllowedPaths = task.Plan.Where
	task.Plan.Checks = []model.Check{{Name: "regression", Command: project.Policy.Checks[0].Command}}
	seed("ideation operation", db.Put(ctx, "operations", ideationID, model.Operation{Kind: "ideation", Subject: project.ID, Stage: "complete", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}, 0))
	idea := model.Idea{ID: ideaID, ProjectID: project.ID, RunID: ideationID, Title: "Repair the result", Direction: "harden", What: "Correct the result", Why: "The result can be wrong",
		Where: "result.txt", HowItHelps: "Returns a useful value", NoveltyMethod: "not_checked", NoveltyOutcome: "deferred", DecisionStatus: "draft"}
	seed("idea draft", db.Put(ctx, "ideas", ideaID, idea, 0))
	idea.DecisionStatus = "approved"
	seed("approved idea", db.Put(ctx, "ideas", ideaID, idea, 1))
	task.Stage = "planning"
	seed("plan intake", db.Put(ctx, "execution_plans", task.ID, task, 0))
	now := time.Now().UTC()
	planner := model.Attempt{AttemptID: "fixture-planner", Task: task.ID, ProjectID: project.ID, IdeaID: ideaID, OperationID: ideationID,
		Role: "plan", PlanVersion: 1, Status: "running", StartedAt: now.Format(time.RFC3339Nano), DeadlineAt: now.Add(time.Minute).Format(time.RFC3339Nano)}
	seed("plan claim", db.Put(ctx, "sessions", planner.AttemptID, planner, 0))
	task.LastAttemptID = planner.AttemptID
	seed("plan document", e.WriteLocalPlan(ctx, project, &task))
	seed("planner document location", db.PlannerRunLocation(ctx, planner.AttemptID, task.PlanDocument.Path))
	planner.Status, planner.Finished, planner.FinishedAt = model.AttemptAwaitingPlanPublication, true, time.Now().UTC().Format(time.RFC3339Nano)
	seed("seeded planner result", db.Put(ctx, "sessions", planner.AttemptID, planner, 1))
	seed("plan handoff", db.CommitPlannerPublication(ctx, task, 1, func() error { return nil }))
	seed("plan approval", e.ReviewLocalPlan(ctx, task.ID, 2, "approve", task.PlanDocument.Hash, ""))
	record, err := db.Get(ctx, "execution_plans", task.ID)
	seed("approved plan lookup", err)
	task, err = store.Decode[model.ExecutionPlan](record)
	seed("approved plan decode", err)
	task.Stage = "review"
	seed("candidate stage", db.Put(ctx, "execution_plans", task.ID, task, record.Version))
	return task
}

func awaitManagedOfflineResult(t *testing.T, db *store.Store, data, plan, commit, stage string, project model.Project) verification.JobReport {
	t.Helper()
	var result verification.JobReport
	waitOfflineFixture(t, data, stage+" verification "+commit, func() bool {
		rows, err := db.List(t.Context(), "verification")
		if err != nil {
			return false
		}
		for _, row := range rows {
			saved, err := store.Decode[struct {
				Stage    string                   `json:"stage"`
				Manifest verification.JobManifest `json:"runner_manifest"`
				Report   verification.JobReport   `json:"runner_report"`
			}](row)
			m := saved.Manifest
			if err != nil || saved.Stage != stage || m.PlanID != plan || m.CandidateCommit != commit || m.ProjectID != project.ID {
				continue
			}
			if m.PolicyHash != project.PolicyHash || m.OfflineImageID != project.OfflineQualification.ImageID {
				t.Fatalf("candidate verification differs from project qualification: %+v", m)
			}
			mustSeedTerminal(t, saved.Report.Validate(m))
			result = saved.Report
			return true
		}
		return false
	})
	return result
}

func waitOfflineFixture(t *testing.T, data, phase string, condition func() bool) {
	t.Helper()
	for deadline := time.Now().Add(110 * time.Second); time.Now().Before(deadline); {
		if condition() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	logs, _ := os.ReadFile(filepath.Join(data, "whirlwind-supervisor.log"))
	t.Fatalf("timed out during %s\n%s", phase, logs)
}
