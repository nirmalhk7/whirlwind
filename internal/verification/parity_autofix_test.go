package verification

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParityAutofixUsesSeparateStagingAndNeverCertifiesModifiedSource(t *testing.T) {
	binary, manifest := parityRunnerFixture(t)
	raw, _ := json.Marshal(manifest)
	var values map[string]any
	requireVerificationSuccess(t, json.Unmarshal(raw, &values))
	values["commands"] = []map[string]any{
		{"name": "fix", "mode": "autofix", "command": []string{"/bin/sh", "-c", "printf 'corrected source  \\n' > new.txt"}, "timeout_seconds": 5},
		{"name": "independent", "command": []string{"/bin/sh", "-c", "test ! -e new.txt"}, "timeout_seconds": 5},
		{"name": "blocked", "command": []string{"/usr/bin/true"}, "depends_on": []string{"fix"}, "timeout_seconds": 5},
	}
	raw, _ = json.Marshal(values)
	command := exec.Command(binary)
	command.Stdin = bytes.NewReader(raw)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("approved autofix job not supported: %v %s", err, output)
	}
	data, err := os.ReadFile(filepath.Join(manifest.Root, "report.json"))
	requireVerificationSuccess(t, err)
	var report struct {
		Passed  bool `json:"passed"`
		Results []struct {
			Name, Status, StartedAt string
			Passed                  bool
			ExitCode                *int `json:"exit_code"`
			Autofix                 *struct {
				PatchPath       string `json:"patch_path"`
				SHA256          string `json:"sha256"`
				CandidateCommit string `json:"candidate_commit"`
			} `json:"autofix"`
		}
	}
	requireVerificationSuccess(t, json.Unmarshal(data, &report))
	if report.Passed || len(report.Results) != 3 || report.Results[0].Status != "pass" || !report.Results[0].Passed || report.Results[0].ExitCode == nil || *report.Results[0].ExitCode != 0 || report.Results[0].Autofix == nil {
		t.Fatalf("raw command outcome/proposed fix not preserved separately from acceptance: %s", data)
	}
	if report.Results[1].Status != "pass" || report.Results[2].Status != "error" {
		t.Fatalf("fix polluted independent checkout or dependencies ran before new snapshot: %s", data)
	}
	proposal := report.Results[0].Autofix
	patch, err := os.ReadFile(proposal.PatchPath)
	if err != nil || !strings.Contains(string(patch), "+corrected source") || proposal.SHA256 == "" || proposal.CandidateCommit != manifest.CandidateCommit {
		t.Fatalf("exact candidate-bound fix patch missing: %q %+v %v", patch, proposal, err)
	}
	for _, root := range []string{manifest.Repository, filepath.Join(manifest.Root, "checkout")} {
		if _, err := os.Stat(filepath.Join(root, "new.txt")); !os.IsNotExist(err) {
			t.Fatalf("autofix mutated frozen/original source: %s %v", root, err)
		}
	}
	assertAutofixPatchReplaysExactBytes(t, manifest.Repository, proposal.PatchPath)
	requireVerificationSuccess(t, os.WriteFile(proposal.PatchPath, []byte("tampered patch\n"), 0600))
	retry := exec.Command(binary)
	retry.Stdin = bytes.NewReader(raw)
	retry.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}
	if output, err := retry.CombinedOutput(); err == nil || !strings.Contains(string(output), "autofix proposal") {
		t.Fatalf("journal recovery accepted altered fix artifact: %v %s", err, output)
	}
}

func assertAutofixPatchReplaysExactBytes(t *testing.T, repository, patchPath string) {
	t.Helper()
	replay := filepath.Join(t.TempDir(), "replay")
	if output, err := exec.Command("git", "clone", "--no-hardlinks", "--", repository, replay).CombinedOutput(); err != nil {
		t.Fatalf("patch replay setup: %v %s", err, output)
	}
	if output, err := exec.Command("git", "-C", replay, "apply", "--binary", patchPath).CombinedOutput(); err != nil {
		t.Fatalf("proposed patch cannot be applied: %v %s", err, output)
	}
	content, err := os.ReadFile(filepath.Join(replay, "new.txt"))
	if err != nil || string(content) != "corrected source  \n" {
		t.Fatalf("autofix patch changed exact bytes: %q %v", content, err)
	}
}
