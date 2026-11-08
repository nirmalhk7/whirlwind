package settingsfile

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestProjectPathsOnboardGitRepositoriesAndReportInvalidPaths(t *testing.T) {
	source := editorSource(t)
	ctx := context.Background()
	repository := t.TempDir()
	repository, err := filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-b", "main", repository).CombinedOutput(); err != nil {
		t.Fatalf("git init: %s %v", out, err)
	}
	invalid := filepath.Join(t.TempDir(), "missing-repository")
	paths, _ := json.Marshal([]string{repository, invalid})
	content := editorYAML + "projects: " + string(paths) + "\n"
	if err := os.WriteFile(source.path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := source.Refresh(ctx); err != nil {
		t.Fatalf("path list could not be loaded: %v", err)
	}
	projects, err := source.store.List(store.WithoutSettingsSource(ctx), "projects")
	if err != nil || len(projects) != 1 {
		t.Fatalf("valid Git repository not onboarded: %+v %v", projects, err)
	}
	project, err := store.Decode[model.Project](projects[0])
	if err != nil || project.Path != repository || project.Name != filepath.Base(repository) || project.DefaultBranch != "main" {
		t.Fatalf("wrong onboarding result: %+v %v", project, err)
	}
	assertProjectStatuses(t, source, map[string]string{repository: "OK", invalid: "ERR"})
	if err := source.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	current, err := source.store.Get(store.WithoutSettingsSource(ctx), "projects", project.ID)
	if err != nil || current.Version != 1 {
		t.Fatalf("refresh re-onboarded unchanged project: %+v %v", current, err)
	}
}

func TestProjectPathsSurviveOtherSettingsSaves(t *testing.T) {
	source := editorSource(t)
	content := editorYAML + "projects: [/missing/repository]\n"
	if err := os.WriteFile(source.path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := source.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	workers := source.doc.Workers
	workers.GlobalSlots = 6
	if err := source.store.Put(context.Background(), "settings", "global", workers, 1); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(source.path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	fields, err = decodeYAML(raw)
	if err != nil {
		t.Fatal(err)
	}
	paths, ok := fields["projects"].([]any)
	if !ok || len(paths) != 1 || paths[0] != "/missing/repository" {
		t.Fatalf("settings save lost project paths: %s", raw)
	}
}

func TestRelativeProjectPathCannotOnboardDashboardWorkingDirectory(t *testing.T) {
	source := editorSource(t)
	if err := os.WriteFile(source.path, []byte(editorYAML+"projects: [.]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := source.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	projects, err := source.store.List(context.Background(), "projects")
	if err != nil || len(projects) != 0 {
		t.Fatalf("relative path unexpectedly onboarded working directory: %+v %v", projects, err)
	}
}

func TestDuplicateProjectPathsOnboardOnce(t *testing.T) {
	source := editorSource(t)
	repository := t.TempDir()
	if out, err := exec.Command("git", "init", repository).CombinedOutput(); err != nil {
		t.Fatalf("git init: %s %v", out, err)
	}
	paths, _ := json.Marshal([]string{repository, repository})
	if err := os.WriteFile(source.path, []byte(editorYAML+"projects: "+string(paths)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := source.Refresh(context.Background()); err != nil {
		t.Fatalf("duplicate path broke settings refresh: %v", err)
	}
	projects, err := source.store.List(context.Background(), "projects")
	if err != nil || len(projects) != 1 {
		t.Fatalf("expected single registered project: %+v %v", projects, err)
	}
}

func assertProjectStatuses(t *testing.T, source *Source, expected map[string]string) {
	t.Helper()
	statuses, err := source.store.List(store.WithoutSettingsSource(context.Background()), "project_status")
	if err != nil || len(statuses) != 1 {
		t.Fatalf("configured paths not inspectable: %+v %v", statuses, err)
	}
	observed := map[string]string{}
	for _, saved := range statuses {
		var paths []struct{ Path, Status string }
		if err := json.Unmarshal(saved.Data, &paths); err != nil {
			t.Fatal(err)
		}
		for _, status := range paths {
			observed[status.Path] = status.Status
		}
	}
	if !reflect.DeepEqual(observed, expected) {
		t.Fatalf("Git path statuses wrong: %v", observed)
	}
}
