package verification

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const offlineFixtureImage = "golang@sha256:87c65a688975c0b1d4585d204874c45ca78354670e3a5f11368c5a492303d696"

func TestOfflineDockerManifestRequiresExplicitNarrowAuthority(t *testing.T) {
	m := JobManifest{Version: 1, JobID: "test", PlanID: "plan", PlanVersion: 1, PlanHash: "plan-hash",
		ConfigurationHash: "configuration", Root: "/verification", Repository: "/repository",
		CandidateCommit: strings.Repeat("1", 40), CandidateTree: strings.Repeat("2", 40), BaseCommit: strings.Repeat("3", 40),
		OfflineDockerImage: offlineFixtureImage, Commands: []JobCommand{{Name: "test", Command: []string{"go", "test"}, TimeoutSeconds: 30}}}
	requireVerificationSuccess(t, m.validateDefinition())
	for name, change := range map[string]func(*JobManifest){
		"floating tag":     func(m *JobManifest) { m.OfflineDockerImage = "golang:latest" },
		"native launcher":  func(m *JobManifest) { m.Launcher = "/native" },
		"autofix":          func(m *JobManifest) { m.Commands[0].Mode = "autofix" },
		"host environment": func(m *JobManifest) { m.Commands[0].Environment = map[string]string{"PATH": "/some/path"} },
		"writable output":  func(m *JobManifest) { m.Commands[0].OutputPaths = []string{"reports"} },
		"optional command": func(m *JobManifest) { required := false; m.Commands[0].Required = &required },
		"scanner":          func(m *JobManifest) { m.Commands[0].Command = []string{"qlty", "check"} },
	} {
		t.Run(name, func(t *testing.T) {
			bad := m
			bad.Commands = append([]JobCommand(nil), m.Commands...)
			change(&bad)
			if err := bad.validateDefinition(); err == nil {
				t.Fatal("unsupported manifest was accepted")
			}
		})
	}
	if runtime.GOOS == "linux" {
		m.OfflineDockerImage, m.Launcher = "", "/native"
		if err := m.Validate(); err == nil {
			t.Fatal("native Linux verifier was accidentally enabled")
		}
	}
}

func TestOfflineDockerShippedVerifier(t *testing.T) {
	image := os.Getenv("WHIRLWIND_TEST_OFFLINE_DOCKER_IMAGE")
	if image == "" {
		t.Skip("set a locally available immutable Linux Go Alpine image to test managed rootless Docker")
	}
	if runtime.GOOS != "linux" {
		t.Fatal("the physical offline verifier test requires Linux")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	root := t.TempDir()
	repo := filepath.Join(root, "repository")
	requireVerificationSuccess(t, os.Mkdir(repo, 0700))
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
		raw, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture git %v: %v: %s", args, err, raw)
		}
		return strings.TrimSpace(string(raw))
	}
	write := func(path, body string) {
		t.Helper()
		requireVerificationSuccess(t, os.WriteFile(filepath.Join(repo, path), []byte(body), 0600))
	}
	commit := func() (string, string) {
		git("add", "--all")
		git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-qm", "fixture")
		return git("rev-parse", "HEAD"), git("rev-parse", "HEAD^{tree}")
	}
	git("init", "-q")
	write(".gitattributes", "go.mod filter=canary export-ignore\n")
	write("go.mod", "module example.test/offline\ngo 1.23.0\n")
	write("result.go", "package result\nfunc Result() bool { return false }\n")
	base, baseTree := commit()
	write("result_test.go", "package result\nimport \"testing\"\nfunc TestResult(t *testing.T) { if !Result() { t.Fatal(\"Result must return true\") } }\n")
	red, redTree := commit()
	write("result.go", "package result\nfunc Result() bool { return true }\n")
	green, greenTree := commit()
	write("untracked-canary", "do not export")
	filterCanary := filepath.Join(root, "filter-was-run")
	git("config", "filter.canary.smudge", "cat; touch '"+filterCanary+"'")
	before := git("status", "--porcelain")
	canaryPath := filepath.Join(root, "outside-canary")
	requireVerificationSuccess(t, os.WriteFile(canaryPath, []byte("synthetic only"), 0600))
	t.Setenv("WHIRLWIND_TEST_OFFLINE_HOST_CANARY", "synthetic only")

	verifier := filepath.Join(root, "whirlwind-verify")
	build := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-o", verifier, "../../cmd/whirlwind-verify")
	if raw, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build shipped verifier: %v: %s", err, raw)
	}
	manifest := func(id, candidate, tree string) JobManifest {
		return JobManifest{Version: 1, JobID: id, PlanID: "approved-plan", PlanVersion: 2, PlanHash: "approved-document",
			ConfigurationHash: "approved-config", CandidateCommit: candidate, CandidateTree: tree, BaseCommit: base,
			Repository: repo, Root: filepath.Join(root, id), OfflineDockerImage: image, Commands: []JobCommand{
				{Name: "test", Command: []string{"go", "test", "-mod=readonly", "-count=1", "-json", "./..."}, TimeoutSeconds: 80, ReportFormat: "go-test-json"},
				{Name: "isolation", Command: []string{"sh", "-eu", "-c", offlineIsolationControl, "control", canaryPath}, TimeoutSeconds: 12},
			}}
	}
	run := func(m JobManifest, expectedStatus string) (JobReport, Result) {
		t.Helper()
		report, err := RunLocalJob(ctx, verifier, m, nil)
		requireVerificationSuccess(t, err)
		check := report.Results[0]
		if check.Status != expectedStatus || !check.Runtime.CleanupVerified || check.Runtime.Kind != "docker-offline" ||
			report.CandidateCommit != m.CandidateCommit || report.CandidateTree != m.CandidateTree {
			t.Fatalf("incorrect command or candidate evidence: %+v", check)
		}
		if report.Results[1].Status != "pass" || !strings.Contains(report.Results[1].Output, "offline controls passed") {
			t.Fatalf("physical isolation controls failed: %+v", report.Results[1])
		}
		value := check.Result
		value.Output, _, err = ReadJobStreams(m.Root, 0)
		requireVerificationSuccess(t, err)
		t.Logf("%s commit=%s tree=%s status=%s container=%s cleaned=%t", m.JobID, m.CandidateCommit, m.CandidateTree, check.Status, check.Runtime.SandboxID, check.Runtime.CleanupVerified)
		return report, value
	}
	_, baseResult := run(manifest("baseline", base, baseTree), "pass")
	redReport, redResult := run(manifest("regression-red", red, redTree), "fail")
	if redReport.Passed || !IsStructuredTestFailure(redResult) {
		t.Fatal("failed assertion was not detected from the actual verifier output")
	}
	greenManifest := manifest("regression-green", green, greenTree)
	greenReport, greenResult := run(greenManifest, "pass")
	requireVerificationSuccess(t, RegressionEvidenceError(baseResult, redResult, greenResult))
	stale := greenManifest
	stale.CandidateCommit = red
	if greenReport.Validate(stale) == nil {
		t.Fatal("report accepted a changed candidate")
	}
	// The same job can reopen its journal, preserving container identity and raw evidence.
	reopened, err := RunLocalJob(ctx, verifier, greenManifest, nil)
	requireVerificationSuccess(t, err)
	if reopened.Results[0].Runtime.SandboxID != greenReport.Results[0].Runtime.SandboxID {
		t.Fatal("completed check ran again on restart")
	}
	requireVerificationSuccess(t, os.WriteFile(filepath.Join(greenManifest.Root, "streams", "000.stdout"), []byte("tampered output"), 0600))
	if greenReport.Validate(greenManifest) == nil {
		t.Fatal("report accepted tampered command output")
	}
	if _, err := RunLocalJob(ctx, verifier, greenManifest, nil); err == nil {
		t.Fatal("shipped verifier replayed or accepted a tampered report")
	}
	missing := manifest("missing-image", green, greenTree)
	missing.OfflineDockerImage = "golang@sha256:" + strings.Repeat("0", 64)
	if _, err := RunLocalJob(ctx, verifier, missing, nil); err == nil || !strings.Contains(err.Error(), "already be present locally") {
		t.Fatalf("missing image did not fail closed: %v", err)
	}
	expired := manifest("expired", green, greenTree)
	expired.Commands = []JobCommand{{Name: "deadline", Command: []string{"sh", "-c", "printf started; sleep 30"}, TimeoutSeconds: 3}}
	lapsed, err := RunLocalJob(ctx, verifier, expired, nil)
	requireVerificationSuccess(t, err)
	if lapsed.Passed || lapsed.Results[0].Status != "timeout" || !lapsed.Results[0].Runtime.CleanupVerified {
		t.Fatalf("expired container remained eligible or was not reaped: %+v", lapsed.Results[0])
	}
	if git("status", "--porcelain") != before || git("rev-parse", "HEAD") != green {
		t.Fatal("offline verifier changed the input checkout")
	}
	canary, err := os.ReadFile(canaryPath)
	requireVerificationSuccess(t, err)
	if _, err := os.Stat(filterCanary); !os.IsNotExist(err) {
		t.Fatalf("host Git smudge filter executed: %v", err)
	}
	if string(canary) != "synthetic only" {
		t.Fatal("outside canary changed")
	}
}

func TestOfflineDockerExportRejectsOmittedAndTranslatedContent(t *testing.T) {
	const blobID = "ce013625030ba8dba906f756967f9e9ca394464a" // Git blob of hello plus newline.
	full := blobID + " blob 6\nhello\n\n"
	for name, test := range map[string]struct {
		data   string
		accept bool
	}{"complete": {full, true}, "omitted": {}, "trailing": {full + "extra", false}, "changed": {strings.Replace(full, "hello", "HELLO", 1), false}} {
		t.Run(name, func(t *testing.T) {
			files, err := offlineTreeBlobs("100644 blob " + blobID + "\thello.txt\x00")
			requireVerificationSuccess(t, err)
			if err := unpackOfflineBlobs(t.Context(), t.TempDir(), strings.NewReader(test.data), files); (err == nil) != test.accept {
				t.Fatalf("export acceptance=%t: %v", test.accept, err)
			}
		})
	}
}

const offlineIsolationControl = `
test -r go.mod
test ! -e .git
test ! -e untracked-canary
test ! -e "$1"
test ! -S /var/run/docker.sock
test -z "${WHIRLWIND_TEST_OFFLINE_HOST_CANARY+x}"
test ! -w /etc/hosts
test ! -w /etc/resolv.conf
if (printf "changed\n" >> go.mod) 2>/dev/null; then exit 19; fi
if touch /checkout/write-canary 2>/dev/null; then exit 20; fi
if nc -w 1 192.0.2.1 443 </dev/null; then exit 21; fi
test -z "$(ip route)"
printf "private scratch\n" >/tmp/scratch-canary
printf "offline controls passed\n"
`
