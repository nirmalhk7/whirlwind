package provider

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
)

func TestProvisionACPAuthGrantsOnlySelectedProfileCredential(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeNativeFixture(t, filepath.Join(home, ".codex/auth.json"), `{"token":"private"}`)
	writeNativeFixture(t, filepath.Join(home, ".codex/config.toml"), "unrelated integration config")
	t.Setenv("OPENAI_API_KEY", "authorized-env-value-that-is-long-enough")
	profile := execution.Profile{CWD: t.TempDir(), Environment: []string{"OPENAI_API_KEY", "OTHER_TOKEN"}, NativeFiles: []execution.NativeFile{}}
	provider := model.Provider{ID: "codex", Kind: "acp", ACPProfile: "codex"}

	if err := provisionACPAuth(&profile, provider); err != nil {
		t.Fatal(err)
	}
	resolvedHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	wantSource := filepath.Join(resolvedHome, ".codex/auth.json")
	want := execution.NativeFile{Source: wantSource, Home: "CODEX_HOME", Path: "auth.json"}
	if len(profile.NativeFiles) != 1 || profile.NativeFiles[0] != want {
		t.Fatalf("auth grant includes unrelated native state: %+v", profile.NativeFiles)
	}
	if !slices.Contains(profile.ReadRoots, wantSource) || !slices.Contains(profile.Environment, "OPENAI_API_KEY") || slices.Contains(profile.Environment, "CODEX_API_KEY") {
		t.Fatalf("auth grant did not preserve bounded source/environment selection: %+v", profile)
	}
	if got := os.Getenv("OPENAI_API_KEY"); got != "authorized-env-value-that-is-long-enough" {
		t.Fatalf("test environment changed: %q", got)
	}
}

func TestProvisionACPAuthRejectsMissingAuthorizedCredentials(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	profile := execution.Profile{CWD: t.TempDir(), Environment: []string{}}
	err := provisionACPAuth(&profile, model.Provider{ID: "claude", Kind: "acp", ACPProfile: "claude"})
	if err == nil || !strings.Contains(err.Error(), "claude") || !strings.Contains(err.Error(), "authenticate") {
		t.Fatalf("missing actionable auth setup error: %v", err)
	}
}

func TestProvisionACPAuthAcceptsOnlyExplicitlyAllowedProfileEnvironment(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "authorized-claude-secret-reference")
	profile := execution.Profile{CWD: t.TempDir(), Environment: []string{"CLAUDE_CODE_OAUTH_TOKEN"}}
	if err := provisionACPAuth(&profile, model.Provider{ID: "claude", Kind: "acp", ACPProfile: "claude"}); err != nil {
		t.Fatal(err)
	}
	if len(profile.NativeFiles) != 0 || !slices.Equal(profile.Environment, []string{"CLAUDE_CODE_OAUTH_TOKEN"}) {
		t.Fatalf("environment auth grant broadened profile authority: %+v", profile)
	}
}

func TestProvisionACPAuthRejectsCredentialSymlinks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "cli-config.json")
	writeNativeFixture(t, outside, `{"auth":"private"}`)
	if err := os.Symlink(outside, filepath.Join(home, ".cursor/cli-config.json")); err != nil {
		t.Fatal(err)
	}
	profile := execution.Profile{CWD: t.TempDir()}
	err := provisionACPAuth(&profile, model.Provider{ID: "cursor", Kind: "acp", ACPProfile: "cursor"})
	if err == nil || len(profile.NativeFiles) != 0 {
		t.Fatalf("credential symlink was granted: error=%v files=%+v", err, profile.NativeFiles)
	}
}

func TestProvisionACPAuthUsesLinuxProviderConfigHome(t *testing.T) {
	for _, test := range []struct {
		id, source, home, path string
	}{
		{"codex", ".codex/auth.json", "CODEX_HOME", "auth.json"},
		{"claude", ".claude/.credentials.json", "CLAUDE_CONFIG_DIR", ".credentials.json"},
		{"cursor", ".cursor/cli-config.json", "HOME", ".cursor/cli-config.json"},
	} {
		t.Run(test.id, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			writeNativeFixture(t, filepath.Join(home, test.source), `{"credential":"private"}`)
			profile := execution.Profile{CWD: t.TempDir()}
			if err := provisionACPAuth(&profile, model.Provider{ID: test.id, Kind: "acp", ACPProfile: test.id}); err != nil {
				t.Fatal(err)
			}
			if len(profile.NativeFiles) != 1 || profile.NativeFiles[0].Home != test.home || profile.NativeFiles[0].Path != test.path {
				t.Fatalf("credential does not match sandbox profile home: %+v", profile.NativeFiles)
			}
		})
	}
}

func TestProviderPreparationAppliesACPAuthBeforeLaunch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeNativeFixture(t, filepath.Join(home, ".codex/auth.json"), `{"token":"private"}`)
	profile := execution.Profile{CWD: t.TempDir(), Environment: []string{}}
	session := &providerSession{client: NewClient(Input{}, Hooks{})}
	if _, err := session.prepareProviderIntegrations(model.Provider{Kind: "acp", ACPProfile: "codex"}, model.Provider{Kind: "acp", ID: "codex", ACPProfile: "codex"}, &profile); err != nil {
		t.Fatal(err)
	}
	if len(profile.NativeFiles) != 1 || filepath.Base(profile.NativeFiles[0].Source) != "auth.json" {
		t.Fatalf("ACP command would start without its bounded credential grant: %+v", profile.NativeFiles)
	}
}

func TestProviderPreparationLeavesUnprofiledACPAuthExplicit(t *testing.T) {
	profile := execution.Profile{CWD: t.TempDir(), Environment: []string{}}
	session := &providerSession{client: NewClient(Input{}, Hooks{})}
	if _, err := session.prepareProviderIntegrations(model.Provider{Kind: "acp"}, model.Provider{Kind: "acp", ID: "custom"}, &profile); err != nil {
		t.Fatalf("unprofiled custom ACP command should use only its explicit runtime authorization: %v", err)
	}
	if len(profile.NativeFiles) != 0 {
		t.Fatalf("unprofiled ACP command received guessed native credentials: %+v", profile.NativeFiles)
	}
}
