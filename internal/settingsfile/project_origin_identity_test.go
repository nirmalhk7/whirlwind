package settingsfile

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/projectregistry"
	"whirlwind/internal/store"
)

const fixtureOrigin = "https://github.com/example/fixture-repository"

func projectOriginGit(t *testing.T, path string, args ...string) {
	t.Helper()
	if output, err := exec.Command("git", append([]string{"-C", path}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func savedHealthBindings(t *testing.T, source *Source) []model.ProjectStatus {
	t.Helper()
	record, err := source.store.Get(t.Context(), "project_status", "configured")
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := store.Decode[[]model.ProjectStatus](record)
	if err != nil {
		t.Fatal(err)
	}
	return bindings
}

func TestOriginChangesKeepSameConfiguredCheckoutAndHistory(t *testing.T) {
	source := editorSource(t)
	repository := healthRepository(t)
	alias := filepath.Join(t.TempDir(), "repo-link")
	if err := os.Symlink(repository, alias); err != nil {
		t.Fatal(err)
	}
	refreshHealthProjects(t, source, repository, alias)
	previous := saveHumanProjectChoices(t, source)
	project, err := store.Decode[model.Project](previous)
	if err != nil {
		t.Fatal(err)
	}
	project.PolicyHash = "human-confirmed-policy"
	project.QualityIdentity = &model.QualityIdentity{Version: 1, QltyVersion: "accepted-scanner"}
	if err := source.store.Put(t.Context(), "projects", project.ID, project, previous.Version); err != nil {
		t.Fatal(err)
	}
	if err := source.store.Put(t.Context(), "operations", "historical", model.Operation{
		Kind: "ideation", Subject: project.ID, Stage: "complete",
	}, 0); err != nil {
		t.Fatal(err)
	}
	operations, err := source.store.List(t.Context(), "operations")
	if err != nil {
		t.Fatal(err)
	}
	assertRefresh := func(repo, remote string, expectedVersion int) {
		t.Helper()
		if err := source.Refresh(t.Context()); err != nil {
			t.Fatal(err)
		}
		records, err := source.store.List(t.Context(), "projects")
		if err != nil || len(records) != 1 || records[0].ID != project.ID {
			t.Fatalf("origin changed checkout identity: %+v, %v", records, err)
		}
		got, err := store.Decode[model.Project](records[0])
		want := project
		want.Repo, want.RemoteURL = repo, remote
		if err != nil || !reflect.DeepEqual(got, want) || records[0].Version != expectedVersion {
			t.Fatalf("unexpected metadata or lost human state: got %+v (v%d), want %+v (v%d): %v", got, records[0].Version, want, expectedVersion, err)
		}
		bindings := savedHealthBindings(t, source)
		if len(bindings) != 2 {
			t.Fatal("configured path aliases were lost")
		}
		for _, binding := range bindings {
			if binding.ProjectID != project.ID || binding.CanonicalPath != repository || binding.Status != "OK" {
				t.Fatalf("configured alias no longer selects the same project: %+v", binding)
			}
		}
		after, err := source.store.List(t.Context(), "operations")
		if err != nil || !reflect.DeepEqual(after, operations) {
			t.Fatalf("origin update duplicated work or lost history: %+v, %v", after, err)
		}
	}
	projectOriginGit(t, repository, "remote", "add", "origin", "git@github.com:example/fixture-repository.git")
	assertRefresh(fixtureOrigin, fixtureOrigin, previous.Version+2)
	assertRefresh(fixtureOrigin, fixtureOrigin, previous.Version+2)
	projectOriginGit(t, repository, "remote", "remove", "origin")
	assertRefresh(repository, "", previous.Version+3)
}

func TestRetargetedAliasDoesNotReuseProjectFromDifferentCheckout(t *testing.T) {
	source := editorSource(t)
	original, newRepository := healthRepository(t), healthRepository(t)
	alias := filepath.Join(t.TempDir(), "repo-link")
	if err := os.Symlink(original, alias); err != nil {
		t.Fatal(err)
	}
	refreshHealthProjects(t, source, alias)
	initial := savedHealthBindings(t, source)[0]
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(newRepository, alias); err != nil {
		t.Fatal(err)
	}
	if err := source.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	current := savedHealthBindings(t, source)[0]
	if current.ProjectID == initial.ProjectID || current.CanonicalPath != newRepository || current.Status != "OK" {
		t.Fatalf("retargeted symlink preserved the wrong project: before=%+v after=%+v", initial, current)
	}
	if _, err := source.store.Get(t.Context(), "projects", initial.ProjectID); err != nil {
		t.Fatalf("previous project history was removed: %v", err)
	}
}

func TestConfiguredPathReusesPreviouslyRegisteredCheckoutWithoutBinding(t *testing.T) {
	source := editorSource(t)
	repo := healthRepository(t)
	existing := model.Project{ID: "existing", Name: "Previously registered", Path: repo, Repo: repo, Status: "approved"}
	if err := source.store.Put(t.Context(), "projects", existing.ID, existing, 0); err != nil {
		t.Fatal(err)
	}
	projectOriginGit(t, repo, "remote", "add", "origin", fmt.Sprintf("%s.git", fixtureOrigin))
	refreshHealthProjects(t, source, repo)
	records, err := source.store.List(t.Context(), "projects")
	if err != nil || len(records) != 1 || records[0].ID != existing.ID {
		t.Fatalf("configured path did not reuse the existing checkout: %+v, %v", records, err)
	}
	updated, err := store.Decode[model.Project](records[0])
	if err != nil || updated.RemoteURL != fixtureOrigin || updated.Name != existing.Name || updated.Status != existing.Status {
		t.Fatalf("existing project did not receive a metadata-only update: %+v, %v", updated, err)
	}
	operations, err := source.store.List(t.Context(), "operations")
	if err != nil || len(operations) != 0 {
		t.Fatalf("existing checkout was put through onboarding again: %+v, %v", operations, err)
	}
}

func TestOriginRefreshDoesNotOverwriteConcurrentProjectChanges(t *testing.T) {
	source := editorSource(t)
	repo := healthRepository(t)
	refreshHealthProjects(t, source, repo)
	binding := savedHealthBindings(t, source)[0]
	ctx := store.WithoutSettingsSource(t.Context())
	projectOriginGit(t, repo, "remote", "add", "origin", fixtureOrigin+".git")
	candidate, err := projectregistry.Load(ctx, "New metadata", repo)
	if err != nil {
		t.Fatal(err)
	}
	changes := &configurationChanges{source: source, ctx: ctx}
	if _, err := changes.preserveCheckoutIdentity(candidate, binding); err != nil {
		t.Fatal(err)
	}
	record, err := source.store.Get(ctx, "projects", binding.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	later, err := store.Decode[model.Project](record)
	if err != nil {
		t.Fatal(err)
	}
	later.Name, later.Paused = "Latest human choice", true
	if err := source.store.Put(ctx, "projects", record.ID, later, record.Version); err != nil {
		t.Fatal(err)
	}
	if err := source.store.Write(ctx, changes.values, nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale metadata update could overwrite a human edit: %v", err)
	}
	if err := source.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	record, err = source.store.Get(ctx, "projects", binding.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Decode[model.Project](record)
	later.RemoteURL, later.Repo = fixtureOrigin, fixtureOrigin
	if err != nil || !reflect.DeepEqual(saved, later) {
		t.Fatalf("retry failed to merge only remote metadata: saved=%+v want=%+v err=%v", saved, later, err)
	}
}

func TestHostedRepositoryKeyRemainsStableAcrossEquivalentOriginMetadata(t *testing.T) {
	source := editorSource(t)
	repo := healthRepository(t)
	projectOriginGit(t, repo, "remote", "add", "origin", fixtureOrigin+".git")
	legacy := model.Project{ID: "existing", Name: "Legacy registered", Path: repo, Repo: "example/fixture-repository", Status: "approved"}
	if err := source.store.Put(t.Context(), "projects", legacy.ID, legacy, 0); err != nil {
		t.Fatal(err)
	}
	refreshHealthProjects(t, source, repo)
	record, err := source.store.Get(t.Context(), "projects", legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Decode[model.Project](record)
	if err != nil || saved.Repo != legacy.Repo || saved.RemoteURL != fixtureOrigin {
		t.Fatalf("equivalent remote metadata changed the issue repository key: %+v, %v", saved, err)
	}
}
