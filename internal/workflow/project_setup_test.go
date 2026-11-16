package workflow

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/policy"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

type setupExecutor interface {
	runProjectSetup(context.Context, string) error
	dispatchProjectSetup(context.Context)
}

func TestProjectSetupRunsHumanCommandsBeforeReadiness(t *testing.T) {
	for _, failing := range []bool{false, true} {
		name := "success"
		if failing {
			name = "required failure"
		}
		t.Run(name, func(t *testing.T) {
			e, _, repository := testEngine(t)
			runner, ok := any(e).(setupExecutor)
			if !ok {
				t.Fatal("supervisor does not execute durable project setup")
			}
			installSetupRunner(t)
			command := "/usr/bin/true"
			if failing {
				command = "/usr/bin/false"
			}
			configuration := "checks:\n  - name: first\n    command: [" + command + "]\n    timeout_seconds: 10\n  - name: later\n    command: [/usr/bin/true]\n    timeout_seconds: 10\nallowed_paths: ['**']\nrisk_rules:\n  - level: low\n    paths: ['**']\n"
			if err := os.WriteFile(filepath.Join(repository, ".whirlwind.yaml"), []byte(configuration), 0600); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"add", ".whirlwind.yaml"}, {"-c", "core.hooksPath=/dev/null", "commit", "-m", "Confirm project verification commands"}} {
				if out, err := exec.Command("git", append([]string{"-C", repository}, args...)...).CombinedOutput(); err != nil {
					t.Fatalf("commit policy: %v %s", err, out)
				}
			}
			project, err := e.Project(context.Background(), "p")
			requireWorkflowSuccess(t, err)
			project.Policy, project.PolicyHash, err = policy.Load(repository)
			requireWorkflowSuccess(t, err)
			project.Status = "draft"
			requireWorkflowSuccess(t, e.Store.Put(context.Background(), "projects", "p", project, 1))
			id := store.ID()
			op := model.Operation{Kind: "project_setup", Subject: "p", Stage: "queued", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
			requireWorkflowSuccess(t, e.Store.Put(context.Background(), "operations", id, op, 0))
			runner.dispatchProjectSetup(context.Background())
			e.background.Wait()
			record, err := e.Store.Get(context.Background(), "operations", id)
			requireWorkflowSuccess(t, err)
			op, err = store.Decode[model.Operation](record)
			requireWorkflowSuccess(t, err)
			var evidence struct {
				Manifest verification.JobManifest `json:"manifest"`
				Report   verification.JobReport   `json:"report"`
			}
			raw, _ := json.Marshal(op.Input)
			requireWorkflowSuccess(t, json.Unmarshal(raw, &evidence))
			wantStage, wantStatus := "complete", "approved"
			if failing {
				wantStage, wantStatus = "failed", "error"
			}
			project, err = e.Project(context.Background(), "p")
			requireWorkflowSuccess(t, err)
			if op.Stage != wantStage || project.Status != wantStatus {
				t.Fatalf("setup outcome not reflected truthfully: operation=%+v project=%+v", op, project)
			}
			if evidence.Manifest.OperationID != id || evidence.Manifest.ProjectID != "p" || evidence.Manifest.PlanID != "" || evidence.Manifest.Purpose != "project_setup" {
				t.Fatalf("setup fabricated plan authority or lost identity: %+v", evidence.Manifest)
			}
			if len(evidence.Report.Results) != 2 || evidence.Report.Results[1].Status != "pass" || evidence.Report.Passed == failing {
				t.Fatalf("human commands not all executed: %+v", evidence.Report)
			}
			requireWorkflowSuccess(t, evidence.Report.Validate(evidence.Manifest))
			requireWorkflowSuccess(t, runner.runProjectSetup(context.Background(), id))
			again, err := e.Store.Get(context.Background(), "operations", id)
			requireWorkflowSuccess(t, err)
			if again.Version != record.Version {
				t.Fatal("terminal setup was rerun")
			}
		})
	}
}

func installSetupRunner(t *testing.T) {
	t.Helper()
	_, source, _, _ := runtime.Caller(0)
	repository := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	binary := filepath.Join(t.TempDir(), "whirlwind-verify")
	command := exec.Command("go", "build", "-o", binary, "./cmd/whirlwind-verify")
	command.Dir = repository
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build verifier: %v %s", err, output)
	}
	t.Setenv("WHIRLWIND_VERIFY_BIN", binary)
	t.Setenv("WHIRLWIND_EXECUTION_LAUNCHER", filepath.Join(repository, "bridges", "nono", "launch.mjs"))
}
