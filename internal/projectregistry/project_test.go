package projectregistry

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistrationRejectsBlankProjectName(t *testing.T) {
	root := lifecycleRepository(t, "")
	for _, name := range []string{"", " \t\n", "\u2003\u00a0"} {
		t.Run(name, func(t *testing.T) {
			project, err := Load(context.Background(), name, root)
			if err == nil || !strings.Contains(err.Error(), "project name is required") {
				t.Fatalf("blank name registered project %q: %v", project.ID, err)
			}
		})
	}
}

func TestRegistrationLifecycleReflectsConfirmedSetup(t *testing.T) {
	for _, tc := range []struct{ name, content, want string }{
		{"pending", "", "draft"},
		{"confirmed", "checks:\n  - name: test\n    command: [sh, -c, 'exit 0']\nallowed_paths: ['**']\nrisk_rules:\n  - level: high\n    paths: ['auth/**']\n", "approved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := lifecycleRepository(t, tc.content)
			project, err := Load(context.Background(), "Project", root)
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(project)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			if fields["status"] != tc.want {
				t.Fatalf("registered status=%v want %s", fields["status"], tc.want)
			}
		})
	}
}

func lifecycleRepository(t *testing.T, content string) string {
	t.Helper()
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "--quiet")
	if content != "" {
		if err := os.WriteFile(filepath.Join(root, ".whirlwind.yaml"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		run("add", ".whirlwind.yaml")
		run("-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "--quiet", "-m", "Confirm execution setup")
	}
	return root
}
