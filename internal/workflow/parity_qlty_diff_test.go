package workflow

import (
	"context"
	"os"
	"strings"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

func TestParityQltyHeadDiffPreservesDependentCandidateChecks(t *testing.T) {
	quality := cleanQltyFixtureCheck(t)
	after := model.Check{Name: "after-quality", Command: []string{"sh", "-c", "test -f result.txt"}, DependsOn: []string{quality.Name}, TimeoutSeconds: 5}
	e, project, plan, head := optionalCheckFixture(t, "0", quality, after)
	ctx := context.Background()
	if err := e.verify(ctx, project, &plan); err != nil {
		t.Fatal(err)
	}
	step := taskStep{Engine: e, ctx: ctx, project: project, task: plan}
	if err := step.validateReviewRunnerReport(head); err != nil {
		t.Fatal(err)
	}
	key := qualityBaselineKey(plan, evidenceDigest(project.Policy), structuredChecks(project.Policy.Checks))
	row, err := e.Store.Get(ctx, "quality_baselines", key)
	if err != nil {
		t.Fatal(err)
	}
	base, err := store.Decode[verification.Report](row)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range base.Results {
		if result.Name == quality.Name || result.Name == after.Name {
			t.Fatal("HEAD-diff dependency chain ran against unchanged base")
		}
	}
}

func TestParityQltyHeadDiffNeedsNoScannerBaseline(t *testing.T) {
	check := cleanQltyFixtureCheck(t)
	script, err := os.ReadFile(check.Command[0])
	if err != nil {
		t.Fatal(err)
	}
	guard := "#!/bin/sh\nif [ ! -f result.txt ]; then echo 'HEAD-diff scanner must not run against unchanged base' >&2; exit 77; fi\n"
	if err := os.WriteFile(check.Command[0], []byte(strings.Replace(string(script), "#!/bin/sh\n", guard, 1)), 0700); err != nil {
		t.Fatal(err)
	}
	e, project, plan, head := optionalCheckFixture(t, "0", check)
	ctx := context.Background()
	if err := e.verify(ctx, project, &plan); err != nil {
		t.Fatal(err)
	}
	key := qualityBaselineKey(plan, evidenceDigest(project.Policy), structuredChecks(project.Policy.Checks))
	row, err := e.Store.Get(ctx, "quality_baselines", key)
	if err != nil {
		t.Fatalf("ordinary base evidence unavailable after HEAD-diff verification: %v", err)
	}
	base, err := store.Decode[verification.Report](row)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range base.Results {
		if result.Name == check.Name {
			t.Fatal("HEAD-diff scanner was executed against unchanged base")
		}
	}
	step := taskStep{Engine: e, ctx: ctx, project: project, task: plan}
	if err := step.validateReviewRunnerReport(head); err != nil {
		t.Fatalf("clean HEAD diff required a scanner baseline: %v", err)
	}
}
