package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

type tddCheck struct {
	RawResult        *verification.Result `json:"raw_result,omitempty"`
	BaselineAccepted bool                 `json:"baseline_accepted,omitempty"`
	Files            map[string]string    `json:"files,omitempty"`
	Task             string               `json:"task"`
	Unit             string               `json:"unit"`
	PlanVersion      int                  `json:"plan_version"`
	CheckID          string               `json:"check_id"`
	Passed           bool                 `json:"passed"`
	Phase            string               `json:"phase"`
	Snapshot         string               `json:"snapshot"`
	Production       string               `json:"production"`
	Tests            string               `json:"tests"`
	AttemptID        string               `json:"attempt_id"`
	OutputArtifact   string               `json:"output_artifact"`
	At               string               `json:"at"`
}

func (a *agentAttempt) runCodingCheck(ctx context.Context, id string) (provider.CheckResult, error) {
	check, err := committedCheck(a.project.Policy, id)
	if err != nil {
		return provider.CheckResult{}, err
	}
	a.checkMu.Lock()
	defer a.checkMu.Unlock()
	run := codingCheckRun{attempt: a, check: check}
	if err = run.prepare(ctx); err != nil {
		return provider.CheckResult{}, err
	}
	if err = run.execute(ctx); err != nil {
		return provider.CheckResult{}, err
	}
	if err = run.persist(ctx); err != nil {
		return provider.CheckResult{}, err
	}
	output := run.output()
	a.event("whirlwind/tdd_check", map[string]any{"phase": run.phase, "result": output})
	return output, nil
}

type codingCheckRun struct {
	attempt    *agentAttempt
	check      model.Check
	current    model.Project
	entry      tddCheck
	report     verification.Report
	result     verification.Result
	artifact   model.Artifact
	phase      string
	gatePassed bool
}

func (r *codingCheckRun) prepare(ctx context.Context) error {
	current, err := r.attempt.trustedProject(ctx, r.attempt.project.ID)
	if err != nil {
		return err
	}
	if current.PolicyHash != r.attempt.project.PolicyHash {
		return errors.New("coding check policy changed")
	}
	r.current = current
	r.entry.Snapshot, r.entry.Production, r.entry.Tests, r.entry.Files, err = codingSnapshotManifest(ctx, r.attempt.input.CWD)
	return err
}

func (r *codingCheckRun) execute(ctx context.Context) error {
	profile, err := execution.ForPolicy(r.attempt.input.CWD, true, r.current.PolicyHash, r.current.Policy.Sandbox)
	if err != nil {
		return err
	}
	checks := structuredChecks([]model.Check{r.check})
	r.report, err = verification.RunObservedWithProfile(ctx, r.attempt.input.CWD, checks, profile, nil)
	if err != nil || len(r.report.Results) != 1 {
		return fmt.Errorf("coding check returned no complete result: %w", err)
	}
	after, _, _, err := codingSnapshot(ctx, r.attempt.input.CWD)
	if err != nil || after != r.entry.Snapshot {
		return errors.New("source changed while coding check ran")
	}
	r.result = r.report.Results[0]
	return nil
}

func (r *codingCheckRun) persist(ctx context.Context) error {
	a := r.attempt
	artifact, err := a.Store.Capture(ctx, a.id, "tdd_check:"+r.check.Name, "text/plain", []byte(r.result.Output), store.MessagePayloadLimit, false)
	if err != nil {
		return err
	}
	r.artifact = artifact
	raw := r.result
	raw.Output, raw.OutputArtifact = "", artifact.ID
	r.entry.RawResult = &raw
	r.phase, err = r.determinePhase(ctx)
	if err != nil {
		return err
	}
	r.entry.Task, r.entry.Unit, r.entry.PlanVersion = a.task.ID, a.input.UnitID, a.task.PlanVersion
	r.entry.CheckID, r.entry.Passed, r.entry.Phase = r.check.Name, r.gatePassed, r.phase
	r.entry.AttemptID, r.entry.OutputArtifact, r.entry.At = a.id, artifact.ID, time.Now().UTC().Format(time.RFC3339Nano)
	return a.Store.Put(ctx, "tdd_checks", store.ID(), r.entry, 0)
}

func (r *codingCheckRun) determinePhase(ctx context.Context) (string, error) {
	a := r.attempt
	// Only structured assertion failures, never crashes/timeouts, establish a red phase.
	originRecord, err := a.Store.Get(ctx, "tdd_origins", codingOriginKey(a.task, a.input.UnitID))
	if err != nil {
		return "", err
	}
	origin, err := store.Decode[codingOrigin](originRecord)
	if err != nil {
		return "", err
	}
	baseline, err := a.qualityBaseline(ctx, r.current, a.task, structuredChecks([]model.Check{r.check}))
	if err != nil {
		return "", err
	}
	phase, err := r.phaseAgainstBaseline(ctx, origin, baseline)
	if err != nil {
		return "", err
	}
	if phase == "green" {
		err = r.advanceOrigin(ctx, originRecord.Version)
	}
	return phase, err
}

func (r *codingCheckRun) advanceOrigin(ctx context.Context, version int) error {
	a := r.attempt
	rows, err := a.Store.List(ctx, "tdd_checks")
	if err != nil {
		return err
	}
	for i := len(rows) - 1; i >= 0; i-- {
		var prior tddCheck
		if json.Unmarshal(rows[i].Data, &prior) != nil || !r.matchesRed(prior) {
			continue
		}
		return a.Store.Put(ctx, "tdd_origins", codingOriginKey(a.task, a.input.UnitID), codingOrigin{Production: r.entry.Production, Tests: r.entry.Tests}, version)
	}
	return nil
}

func (r *codingCheckRun) matchesRed(prior tddCheck) bool {
	a := r.attempt
	sameUnit := prior.Task == a.task.ID && prior.PlanVersion == a.task.PlanVersion && prior.Unit == a.input.UnitID
	sameCheck := prior.CheckID == r.check.Name && prior.Phase == "red"
	implemented := prior.Tests == r.entry.Tests && prior.Production != r.entry.Production
	return sameUnit && sameCheck && implemented
}

func (r *codingCheckRun) output() provider.CheckResult {
	output := string(store.Prefix([]byte(r.result.Output), 4096))
	if r.entry.BaselineAccepted {
		output += "\nBaseline comparison: preexisting failures remain visible; no new or worsened findings. Raw exit status retained."
	}
	return provider.CheckResult{CheckID: r.check.Name, Snapshot: r.entry.Snapshot, Passed: r.gatePassed, Phase: r.phase, BaselineAccepted: r.entry.BaselineAccepted, ExitCode: r.result.ExitCode, Output: output, OutputArtifact: r.artifact.ID, DurationMS: r.result.DurationMS, Truncated: r.result.Truncated}
}

type codingOrigin struct {
	Production string `json:"production"`
	Tests      string `json:"tests"`
}

func tddPhase(origin codingOrigin, production, tests string, result verification.Result, newFailure bool) string {
	if !result.Passed {
		if newFailure && verification.IsStructuredTestFailure(result) && production == origin.Production && tests != origin.Tests {
			return "red"
		}
		return "invalid"
	}
	return "green"
}
func codingOriginKey(t model.Task, unit string) string {
	return fmt.Sprintf("%s:%d:%s", t.ID, t.PlanVersion, unit)
}
func (e *Engine) recordCodingOrigin(ctx context.Context, t model.Task, cwd, unit string) error {
	key := codingOriginKey(t, unit)
	if _, err := e.Store.Get(ctx, "tdd_origins", key); err == nil {
		return nil
	} else if err != store.ErrNotFound {
		return err
	}
	_, production, tests, err := codingSnapshot(ctx, cwd)
	if err != nil {
		return err
	}
	return e.Store.Put(ctx, "tdd_origins", key, codingOrigin{Production: production, Tests: tests}, 0)
}
