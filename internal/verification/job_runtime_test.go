package verification

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApprovedQltyRuntimeMustBeCanonicalAndRetained(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	requireVerificationSuccess(t, err)
	qualityRoot := filepath.Join(root, "quality")
	retained := filepath.Join(qualityRoot, "jobs", "completed-job", "tools")
	outside := filepath.Join(root, "unrelated", "tools")
	alias := filepath.Join(qualityRoot, "jobs", "alias")
	for _, dir := range []string{retained, outside} {
		requireVerificationSuccess(t, os.MkdirAll(dir, 0700))
	}
	requireVerificationSuccess(t, os.Symlink(retained, alias))

	for _, test := range []struct {
		name string
		root string
		path string
		want string
	}{
		{name: "retained", root: qualityRoot, path: retained},
		{name: "missing jobs root", root: root, path: retained, want: "approved Qlty runtime jobs root is unavailable"},
		{name: "missing runtime", root: qualityRoot, path: filepath.Join(root, "missing"), want: "approved Qlty runtime path is missing or non-canonical"},
		{name: "linked runtime", root: qualityRoot, path: alias, want: "approved Qlty runtime path is missing or non-canonical"},
		{name: "unrelated runtime", root: qualityRoot, path: outside, want: "approved Qlty runtime path is outside retained quality job evidence"},
		{name: "wrong directory name", root: qualityRoot, path: filepath.Dir(retained), want: "approved Qlty runtime path is outside retained quality job evidence"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateApprovedQltyRuntimePath(test.root, test.path)
			if test.want == "" {
				requireVerificationSuccess(t, err)
			} else if err == nil || err.Error() != test.want {
				t.Fatalf("runtime path validation = %v, want %q", err, test.want)
			}
		})
	}
}
