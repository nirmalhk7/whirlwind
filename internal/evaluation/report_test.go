package evaluation

import (
	"testing"
	"whirlwind/internal/model"
)

func TestReportCountsFailuresAndUnknownsWithoutClaimingAcceptance(t *testing.T) {
	tokens := int64(30)
	failure := false
	success := true
	task := model.Task{ID: "task", Stage: "pr_created", PR: "https://example.test/pr/1", ChecksPassed: true, Score: 10}
	sample := Sample{Task: task, Verified: true, Usage: model.UsageReport{Attempts: 2, TotalTokens: 30, UnknownTokenAttempts: 1}, Attempts: []model.Attempt{{AttemptID: "failed", Provider: "a", Model: "m", Role: "code", Success: &failure}, {AttemptID: "ok", Provider: "b", Model: "m", Role: "review", Success: &success}}, Entries: []model.UsageEntry{{AttemptID: "failed", Delta: &model.Usage{TotalTokens: &tokens}}, {AttemptID: "ok"}}}
	report := Build([]Sample{sample})
	if report.Attempts != 2 || report.ReportedTokens != 30 || report.UnknownTokenAttempts != 1 || report.VerifiedTasks != 1 || report.CreatedPRs != 1 || report.MergedPRs != 0 {
		t.Fatalf("incorrect totals: %+v", report)
	}
	if report.UserAcceptedTasks != 0 || report.UnknownAcceptanceTasks != 1 || report.Groups[0].FailedAttempts != 1 {
		t.Fatalf("failed attempts/unknown acceptance hidden: %+v", report)
	}
	sample.Decision = "merged"
	report = Build([]Sample{sample})
	if report.MergedPRs != 1 || report.UserAcceptedTasks != 0 {
		t.Fatal("merge incorrectly treated as demonstrated correctness or acceptance")
	}
	sample.Task.Stage = "complete"
	sample.Verified = false
	report = Build([]Sample{sample})
	if report.VerifiedTasks != 0 {
		t.Fatal("stage falsely established verification")
	}
}

func TestComparisonRequiresEquivalentQualityAndCompleteTelemetry(t *testing.T) {
	baseline := Report{CorpusID: "fixture-v1", Tasks: 2, VerifiedTasks: 2, ReportedTokens: 100, Attempts: 2}
	candidate := baseline
	candidate.ReportedTokens = 80
	if !Compare(baseline, candidate).Eligible {
		t.Fatal("complete comparable run not eligible")
	}
	candidate.UnknownTokenAttempts = 1
	if Compare(baseline, candidate).Eligible {
		t.Fatal("incomplete telemetry supports savings claim")
	}
	candidate.UnknownTokenAttempts = 0
	candidate.VerifiedTasks = 1
	if Compare(baseline, candidate).Eligible {
		t.Fatal("lower quality supports savings claim")
	}
}

func TestOutcomeHistoryKeepsMergeAcceptanceAndRegressionDistinct(t *testing.T) {
	report := Build([]Sample{{Task: model.Task{ID: "task", PR: "pr"}, Decisions: []string{"merged", "accepted", "regression", "regression"}}})
	if report.MergedPRs != 1 || report.UserAcceptedTasks != 1 || report.Regressions != 1 || report.UnknownAcceptanceTasks != 0 {
		t.Fatalf("outcome history collapsed: %+v", report)
	}
}
