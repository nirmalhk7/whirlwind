package verification

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"whirlwind/internal/model"
)

const defaultQualityCatalog = `plugins:
  definitions:
    gitleaks:
      known_good_version: 8.29.1
      drivers:
        lint: {}
`

const defaultPreparedQltyPath = ".qlty/qlty.toml"

func defaultQualityManifest(t *testing.T) JobManifest {
	t.Helper()
	root := t.TempDir()
	return JobManifest{Version: 1, Purpose: ProjectSetupPurpose, ProjectID: "project_fixture", JobID: "job", OperationID: "job", ConfigurationHash: "configuration", CandidateCommit: strings.Repeat("a", 40), CandidateTree: strings.Repeat("b", 40), BaseCommit: strings.Repeat("c", 40), Repository: t.TempDir(), Root: filepath.Join(root, "jobs", "job"), Launcher: filepath.Join(root, "bridges", "nono", "launch.mjs"), Quality: &model.QualitySnapshot{Root: root, Files: map[string]string{defaultPreparedQltyPath: "config_version = \"0\"\n"}}, Commands: []JobCommand{{Name: "metadata", Command: []string{"qlty", "--version"}, TimeoutSeconds: 15}}}
}

func TestProjectSetupPreparesFrozenExternalDefaultsWithoutSourceWrites(t *testing.T) {
	manifest := defaultQualityManifest(t)
	snapshot, err := manifest.PrepareQualityDefaults([]byte(defaultQualityCatalog))
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatal(err)
	}
	if snapshot.Root != manifest.Quality.Root || len(snapshot.Files) != 2 {
		t.Fatalf("default quality files or external root missing: %+v", snapshot)
	}
	for _, path := range []string{defaultPreparedQltyPath, ".gitleaks.toml"} {
		content, err := os.ReadFile(filepath.Join(snapshot.Root, "config", path))
		if err != nil || len(content) == 0 || string(content) != snapshot.Files[path] {
			t.Fatalf("default file %s not durably captured: %q %v", path, content, err)
		}
	}
	config := snapshot.Files[defaultPreparedQltyPath]
	if strings.Count(config, "[[plugin]]") != 1 || !strings.Contains(config, "name = \"gitleaks\"\nversion = \"8.29.1\"") {
		t.Fatalf("default setup did not pin only Gitleaks: %s", config)
	}
	if entries, err := os.ReadDir(manifest.Repository); err != nil || len(entries) != 0 {
		t.Fatalf("default setup wrote to original source: %+v %v", entries, err)
	}
	again, err := manifest.PrepareQualityDefaults([]byte(defaultQualityCatalog))
	if err != nil || again.Files[defaultPreparedQltyPath] != snapshot.Files[defaultPreparedQltyPath] {
		t.Fatalf("retry changed prepared defaults: %+v %v", again, err)
	}
}

func TestProjectSetupDefaultsPreserveExistingHumanConfiguration(t *testing.T) {
	manifest := defaultQualityManifest(t)
	path := filepath.Join(manifest.Quality.Root, "config", ".qlty", "qlty.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	want := "# Human-owned configuration\nconfig_version = \"0\"\n"
	if err := os.WriteFile(path, []byte(want), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := manifest.PrepareQualityDefaults([]byte(defaultQualityCatalog))
	if err != nil || snapshot.Files[defaultPreparedQltyPath] != want {
		t.Fatalf("default setup replaced human quality policy: %q %v", snapshot.Files[defaultPreparedQltyPath], err)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != want {
		t.Fatalf("human file changed on disk: %q %v", content, err)
	}
}

func TestProjectSetupDefaultsRejectSourceAndLinkedConfiguration(t *testing.T) {
	for _, location := range []string{"source root", "inside source", "source inside quality root", "root link", "configuration link"} {
		t.Run(location, func(t *testing.T) {
			manifest := unsafeDefaultQualityManifest(t, location)
			if _, err := manifest.PrepareQualityDefaults([]byte(defaultQualityCatalog)); err == nil {
				t.Fatal("unsafe external quality destination accepted")
			}
			if entries, err := os.ReadDir(manifest.Repository); err != nil || len(entries) != 0 {
				t.Fatalf("unsafe destination wrote into source: %+v %v", entries, err)
			}
		})
	}
}

func unsafeDefaultQualityManifest(t *testing.T, location string) JobManifest {
	t.Helper()
	manifest := defaultQualityManifest(t)
	switch location {
	case "source root":
		manifest.Quality.Root = manifest.Repository
	case "inside source":
		manifest.Quality.Root = filepath.Join(manifest.Repository, "quality")
	case "source inside quality root":
		manifest.Repository = filepath.Join(manifest.Quality.Root, "source")
		requireVerificationSuccess(t, os.Mkdir(manifest.Repository, 0700))
	case "root link":
		manifest.Quality.Root = filepath.Join(t.TempDir(), "link")
		requireVerificationSuccess(t, os.Symlink(manifest.Repository, manifest.Quality.Root))
	case "configuration link":
		requireVerificationSuccess(t, os.Symlink(manifest.Repository, filepath.Join(manifest.Quality.Root, "config")))
	}
	manifest.Root = filepath.Join(manifest.Quality.Root, "jobs", "job")
	return manifest
}

func TestApprovedPlanCannotPrepareDefaultProjectConfiguration(t *testing.T) {
	manifest := defaultQualityManifest(t)
	manifest.Purpose, manifest.ProjectID, manifest.OperationID = "", "", ""
	manifest.PlanID, manifest.PlanVersion, manifest.PlanHash = "plan", 1, "approved"
	if _, err := manifest.PrepareQualityDefaults([]byte(defaultQualityCatalog)); err == nil {
		t.Fatal("approved-plan runner rewrote project configuration")
	}
	if _, err := os.Stat(filepath.Join(manifest.Quality.Root, "config")); !os.IsNotExist(err) {
		t.Fatalf("plan preparation wrote project configuration: %v", err)
	}
}

func TestProjectSetupDefaultsRejectIncompleteInputsBeforePublishing(t *testing.T) {
	for _, input := range []struct{ name, catalog string }{
		{"missing catalog", "{}"},
		{"missing version", strings.Replace(defaultQualityCatalog, "known_good_version: 8.29.1", "known_good_version: ''", 1)},
		{"missing lint driver", strings.Replace(defaultQualityCatalog, "lint: {}", "format: {}", 1)},
		{"malformed catalog", "["},
	} {
		t.Run(input.name, func(t *testing.T) {
			manifest := defaultQualityManifest(t)
			if _, err := manifest.PrepareQualityDefaults([]byte(input.catalog)); err == nil {
				t.Fatal("incomplete scanner metadata produced default configuration")
			}
			if _, err := os.Stat(filepath.Join(manifest.Quality.Root, "config")); !os.IsNotExist(err) {
				t.Fatalf("invalid setup input published configuration: %v", err)
			}
		})
	}
}
