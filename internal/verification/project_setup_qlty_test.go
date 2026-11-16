package verification

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	"whirlwind/internal/model"
)

// This checks real CLI/catalog discovery in onboarding isolation. It does not
// certify scanner installation or imply that any language-specific scan ran.
func TestProjectSetupReadsActualQltyCatalogWithoutSourceWrites(t *testing.T) {
	if _, err := exec.LookPath("qlty"); err != nil {
		t.Fatalf("installed Qlty required for setup acceptance: %v", err)
	}
	binary, base := parityRunnerFixture(t)
	manifest := projectSetupFixture(t, base, nil)
	root := t.TempDir()
	manifest.Root = filepath.Join(root, "jobs", "job")
	manifest.Quality = &model.QualitySnapshot{Root: root, Files: map[string]string{
		".qlty/qlty.toml": "config_version = \"0\"\n[[source]]\nname = \"default\"\ndefault = true\n",
	}}
	manifest.Commands = []JobCommand{
		{Name: "version", Command: []string{"qlty", "--version"}, TimeoutSeconds: 15},
		{Name: "catalog", Command: []string{"qlty", "plugins", "list", "--no-upgrade-check"}, TimeoutSeconds: 15},
		{Name: "configuration", Command: []string{"qlty", "config", "show", "--no-upgrade-check"}, TimeoutSeconds: 15},
	}
	report, err := RunLocalJob(context.Background(), binary, manifest, nil)
	requireVerificationSuccess(t, err)
	if !report.Passed {
		t.Fatalf("native setup metadata discovery failed: %+v", report.Results)
	}
	version, _, err := ReadJobStreams(manifest.Root, 0)
	requireVerificationSuccess(t, err)
	if !strings.HasPrefix(version, "qlty ") {
		t.Fatalf("actual Qlty identity missing: %q", version)
	}
	configuration, _, err := ReadJobStreams(manifest.Root, 2)
	requireVerificationSuccess(t, err)
	assertDefaultScannerCatalog(t, configuration)
	if _, err := os.Lstat(filepath.Join(manifest.Repository, ".qlty")); !os.IsNotExist(err) {
		t.Fatalf("setup discovery polluted original repository: %v", err)
	}
}

func assertDefaultScannerCatalog(t *testing.T, configuration string) {
	t.Helper()
	var catalog struct {
		Plugins struct {
			Definitions map[string]struct {
				KnownGoodVersion string         `yaml:"known_good_version"`
				Drivers          map[string]any `yaml:"drivers"`
			} `yaml:"definitions"`
		} `yaml:"plugins"`
	}
	requireVerificationSuccess(t, yaml.Unmarshal([]byte(configuration), &catalog))
	for _, name := range []string{"semgrep", "gitleaks"} {
		definition := catalog.Plugins.Definitions[name]
		if definition.KnownGoodVersion == "" || len(definition.Drivers) == 0 {
			t.Fatalf("default scanner %s lacks usable catalog definition", name)
		}
	}
}
