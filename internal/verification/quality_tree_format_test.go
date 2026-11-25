package verification

import (
	"os"
	"path/filepath"
	"testing"
)

func TestQualityTreeFingerprintRetainsArtifactFormat(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	requireVerificationSuccess(t, os.Mkdir(bin, 0700))
	requireVerificationSuccess(t, os.Chmod(bin, 0700))
	for name, data := range map[string]string{"scanner.lock": "pinned scanner\n", "bin/scanner": "portable fixture\n"} {
		path := filepath.Join(root, filepath.FromSlash(name))
		requireVerificationSuccess(t, os.WriteFile(path, []byte(data), 0700))
		requireVerificationSuccess(t, os.Chmod(path, 0700))
	}
	got, err := hashQualityTree(root)
	requireVerificationSuccess(t, err)
	const want = "45cf6f1f858fd03efaf836e837cfe40da097ac7751338445a3a2a8727a10fa8d"
	if got != want {
		t.Fatalf("quality tree fingerprint = %s, want %s", got, want)
	}
}
