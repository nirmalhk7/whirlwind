package settingsfile

import (
	"context"
	"os"
	"strings"
	"testing"

	"whirlwind/internal/store"
)

func TestAgentHooksRefreshFromSettings(t *testing.T) {
	source := editorSource(t)
	ctx := context.Background()
	file, err := source.File()
	if err != nil {
		t.Fatal(err)
	}
	content := editorYAML + `agent_hooks:
  code:
    after:
      - name: verify
        command: [/usr/bin/true]
`
	save := func(text, revision string) error {
		return source.SaveFile(ctx, text, revision, func(changes []store.Change) error {
			return source.store.Write(store.WithoutSettingsSource(ctx), changes, nil)
		})
	}
	if err := save(content, file.Revision); err != nil {
		t.Fatal(err)
	}
	record, err := source.store.Get(ctx, "workflow_config", "global")
	if err != nil || !strings.Contains(string(record.Data), `"command":["/usr/bin/true"]`) {
		t.Fatalf("missing saved hook command: %s (%v)", record.Data, err)
	}
	if err := os.Remove(source.path); err != nil {
		t.Fatal(err)
	}
	if err := source.ensureFile(store.WithoutSettingsSource(ctx)); err != nil {
		t.Fatal(err)
	}
	file, err = source.File()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(file.Content, "agent_hooks:") || !strings.Contains(file.Content, "/usr/bin/true") {
		t.Fatal("recreating settings dropped active hook commands")
	}
	for _, tt := range []struct{ from, to, want string }{
		{"  code:", "  unsupported:", "unknown agent hook role"},
		{"        command:", "        mode: autofix\n        command:", "mutating check"},
		{"        command:", "        depends_on: [missing]\n        command:", "unknown hook check"},
	} {
		invalid := strings.Replace(content, tt.from, tt.to, 1)
		if err := save(invalid, file.Revision); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Fatalf("want %q, got %v", tt.want, err)
		}
	}
	if err := save(editorYAML, file.Revision); err != nil {
		t.Fatal(err)
	}
	record, err = source.store.Get(ctx, "workflow_config", "global")
	if err != nil || strings.Contains(string(record.Data), `"agent_hooks"`) {
		t.Fatalf("removed hooks still active: %s (%v)", record.Data, err)
	}
}
