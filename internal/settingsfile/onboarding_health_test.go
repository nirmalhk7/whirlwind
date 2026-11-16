package settingsfile

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func TestInvalidProjectSetupIsErrorWithoutBlockingHealthyProjects(t *testing.T) {
	source := editorSource(t)
	broken, healthy := healthRepository(t), healthRepository(t)
	writeBrokenProjectPolicy(t, broken)
	refreshHealthProjects(t, source, broken, healthy)
	assertProjectStatuses(t, source, map[string]string{broken: "ERR", healthy: "OK"})
	assertSetupFailureDetail(t, source, broken, "policy")
	projects, err := source.store.List(context.Background(), "projects")
	if err != nil || len(projects) != 1 {
		t.Fatalf("bad setup blocked healthy registration or registered invalid project: %v %v", projects, err)
	}
	project, err := store.Decode[model.Project](projects[0])
	if err != nil || project.Path != healthy {
		t.Fatalf("wrong healthy project registered: %+v %v", project, err)
	}
}

func TestExistingProjectSetupFailureUpdatesSQLAndPreservesHumanChoices(t *testing.T) {
	for _, alias := range []bool{false, true} {
		t.Run(map[bool]string{false: "canonical path", true: "symlink path"}[alias], func(t *testing.T) {
			assertExistingProjectSetupHealth(t, alias)
		})
	}
}

func assertExistingProjectSetupHealth(t *testing.T, alias bool) {
	t.Helper()
	source := editorSource(t)
	repository := healthRepository(t)
	path := repository
	if alias {
		path = filepath.Join(t.TempDir(), "repository-link")
		if err := os.Symlink(repository, path); err != nil {
			t.Fatal(err)
		}
	}
	refreshHealthProjects(t, source, path)
	before := saveHumanProjectChoices(t, source)
	assertCoreProjectStatus(t, source, before.ID, "complete")
	writeBrokenProjectPolicy(t, repository)
	if err := source.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertCoreProjectStatus(t, source, before.ID, "error")
	assertSetupFailureDetail(t, source, path, "policy")
	after, err := source.store.Get(context.Background(), "projects", before.ID)
	if err != nil || before.Version != after.Version || !bytes.Equal(before.Data, after.Data) {
		t.Fatalf("health refresh overwrote human project choices: before=%+v after=%+v err=%v", before, after, err)
	}
	if err := os.Remove(filepath.Join(repository, ".whirlwind.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := source.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertProjectStatuses(t, source, map[string]string{path: "OK"})
	assertCoreProjectStatus(t, source, before.ID, "complete")
}

func saveHumanProjectChoices(t *testing.T, source *Source) model.Record {
	t.Helper()
	ctx := store.WithoutSettingsSource(context.Background())
	projects, err := source.store.List(ctx, "projects")
	if err != nil || len(projects) != 1 {
		t.Fatalf("existing project missing: %+v %v", projects, err)
	}
	project, err := store.Decode[model.Project](projects[0])
	if err != nil {
		t.Fatal(err)
	}
	project.Name, project.Paused, project.Status = "Human-chosen name", true, "complete"
	if err := source.store.Put(ctx, "projects", project.ID, project, projects[0].Version); err != nil {
		t.Fatal(err)
	}
	before, err := source.store.Get(ctx, "projects", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	return before
}

func assertCoreProjectStatus(t *testing.T, source *Source, id, want string) {
	t.Helper()
	var got string
	if err := source.store.DB.QueryRow("SELECT status FROM projects WHERE project_id=?", id).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("project health status=%q, want %q", got, want)
	}
}

func assertSetupFailureDetail(t *testing.T, source *Source, path, fragment string) {
	t.Helper()
	row, err := source.store.Get(context.Background(), "project_status", "configured")
	if err != nil {
		t.Fatal(err)
	}
	statuses, err := store.Decode[[]model.ProjectStatus](row)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range statuses {
		if status.Path == path && strings.TrimSpace(status.Detail) != "" && strings.Contains(status.Detail, fragment) {
			return
		}
	}
	t.Fatalf("setup failure lost configured path or policy diagnostic: %+v", statuses)
}

func TestBrokenConfiguredAliasRetainsExistingProjectHealthBinding(t *testing.T) {
	source := editorSource(t)
	repository := healthRepository(t)
	alias := filepath.Join(t.TempDir(), "repository-link")
	if err := os.Symlink(repository, alias); err != nil {
		t.Fatal(err)
	}
	refreshHealthProjects(t, source, alias)
	before := saveHumanProjectChoices(t, source)
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := source.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertProjectStatuses(t, source, map[string]string{alias: "ERR"})
	assertCoreProjectStatus(t, source, before.ID, "error")
	assertSetupFailureDetail(t, source, alias, "")
	if err := os.Symlink(repository, alias); err != nil {
		t.Fatal(err)
	}
	if err := source.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertCoreProjectStatus(t, source, before.ID, "complete")
}

func refreshHealthProjects(t *testing.T, source *Source, paths ...string) {
	t.Helper()
	raw, err := json.Marshal(paths)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source.path, []byte(editorYAML+"projects: "+string(raw)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := source.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func healthRepository(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if raw, err := exec.Command("git", "init", "--quiet", root).CombinedOutput(); err != nil {
		t.Fatalf("create project Git fixture: %v %s", err, raw)
	}
	return root
}

func writeBrokenProjectPolicy(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(path, ".whirlwind.yaml"), []byte("checks: [\n"), 0600); err != nil {
		t.Fatal(err)
	}
}
