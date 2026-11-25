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
		want := project
		want.Repo, want.RemoteURL = repo, remote
		assertRefreshedProjectIdentity(t, source, want, expectedVersion)
		assertOriginRefreshBindings(t, source, repository, project.ID)
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

func assertRefreshedProjectIdentity(t *testing.T, source *Source, want model.Project, expectedVersion int) {
	t.Helper()
	records, err := source.store.List(t.Context(), "projects")
	if err != nil || len(records) != 1 || records[0].ID != want.ID {
		t.Fatalf("origin changed checkout identity: %+v, %v", records, err)
	}
	got, err := store.Decode[model.Project](records[0])
	if err != nil || !reflect.DeepEqual(got, want) || records[0].Version != expectedVersion {
		t.Fatalf("unexpected metadata or lost human state: got %+v (v%d), want %+v (v%d): %v", got, records[0].Version, want, expectedVersion, err)
	}
}

func assertOriginRefreshBindings(t *testing.T, source *Source, repository, projectID string) {
	t.Helper()
	bindings := savedHealthBindings(t, source)
	if len(bindings) != 2 {
		t.Fatal("configured path aliases were lost")
	}
	for _, binding := range bindings {
		if binding.ProjectID != projectID || binding.CanonicalPath != repository || binding.Status != "OK" {
			t.Fatalf("configured alias no longer selects the same project: %+v", binding)
		}
	}
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
	changes := &configurationChanges{source: source, ctx: ctx, checkoutIDs: map[string]string{}}
	status, err := changes.onboardProject(repo, binding, false)
	if err != nil || status.Status != "OK" {
		t.Fatalf("stage metadata update: %+v %v", status, err)
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

func TestRefreshArchivesLegacyDuplicateAndReusesBoundCheckoutAfterRestart(t *testing.T) {
	fixture := prepareLegacyCheckout(t)
	history := seedLegacyCheckoutHistory(t, fixture)
	issues := seedLegacyCheckoutIssues(t, fixture)
	restarted := reopenLegacyCheckout(t, fixture)
	hosted := fixture.hosted
	hosted.CheckoutAliases = []string{fixture.old.ID}
	assertLegacyCheckout(t, restarted, hosted, history)
	assertLegacyIssueOwner(t, fixture, issues)

	hosted = assertReplacementOriginIsolation(t, restarted, fixture, hosted, history, issues)
	assertRestoredOriginHistory(t, restarted, fixture, hosted, history, issues)
	assertLegacyCheckoutReferences(t, fixture.source)
}

type legacyCheckoutFixture struct {
	source *Source
	repo   string
	old    model.Record
	hosted model.Project
}

func prepareLegacyCheckout(t *testing.T) legacyCheckoutFixture {
	t.Helper()
	source := editorSource(t)
	ctx := t.Context()
	repo := healthRepository(t)
	alias := filepath.Join(t.TempDir(), "checkout-link")
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	refreshHealthProjects(t, source, repo, alias)
	first := savedHealthBindings(t, source)[0]
	projectOriginGit(t, repo, "remote", "add", "a-mirror", "https://github.com/example/mirror.git")
	projectOriginGit(t, repo, "remote", "add", "origin", fixtureOrigin+".git")
	hosted := model.Project{
		ID: model.ProjectID(fixtureOrigin), Path: repo, Name: "Qualified checkout",
		Repo: "example/fixture-repository", RemoteURL: fixtureOrigin, Status: "approved",
		ManagedPolicy: true, PolicyHash: "confirmed-policy",
		OfflineQualification: &model.OfflineDockerQualification{PolicyHash: "confirmed-policy", JobID: "qualification"},
	}
	if err := source.store.Put(ctx, "projects", hosted.ID, hosted, 0); err != nil {
		t.Fatal(err)
	}
	rows, err := source.store.List(ctx, "projects")
	if err != nil || len(rows) != 2 {
		t.Fatalf("legacy setup must have two registrations: %v, %v", rows, err)
	}
	old, err := source.store.Get(ctx, "projects", first.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	bindings := savedHealthBindings(t, source)
	bindings[0].ProjectID = hosted.ID
	status, err := source.store.Get(ctx, "project_status", "configured")
	if err != nil {
		t.Fatal(err)
	}
	if err := source.store.Put(ctx, "project_status", status.ID, bindings, status.Version); err != nil {
		t.Fatal(err)
	}
	return legacyCheckoutFixture{source: source, repo: repo, old: old, hosted: hosted}
}

func seedLegacyCheckoutHistory(t *testing.T, fixture legacyCheckoutFixture) []model.Record {
	t.Helper()
	ctx, source, old, hosted := t.Context(), fixture.source, fixture.old, fixture.hosted
	for _, id := range []string{old.ID, hosted.ID} {
		for _, kind := range []string{"project_setup", "ideation"} {
			if err := source.store.Put(ctx, "operations", id+"-"+kind, model.Operation{Subject: id, Kind: kind, Stage: "complete"}, 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	idea := model.Idea{
		ID: "historical-idea", ProjectID: old.ID, RunID: old.ID + "-ideation", Direction: "expand",
		Title: "Fixture idea", What: "Add export", Why: "Simplify reporting", Where: "reports",
		HowItHelps: "Create a portable report", DecisionStatus: "draft", NoveltyMethod: "not_checked", NoveltyOutcome: "deferred",
	}
	if err := source.store.Put(ctx, "ideas", idea.ID, idea, 0); err != nil {
		t.Fatal(err)
	}
	history, err := source.store.List(ctx, "operations")
	if err != nil {
		t.Fatal(err)
	}
	return history
}

type legacyCheckoutIssues struct {
	oldIssue, priorIssue     model.Issue
	oldOwnerPlan, plan       model.ExecutionPlan
	ownerSnapshot, priorPlan model.Record
}

func seedLegacyCheckoutIssues(t *testing.T, fixture legacyCheckoutFixture) legacyCheckoutIssues {
	t.Helper()
	ctx, source, old, hosted := t.Context(), fixture.source, fixture.old, fixture.hosted
	oldIssue := model.Issue{Number: 19, Title: "Issue owned by the other identity", State: "OPEN", Labels: []string{"whirlwind:ready"}}
	owner := model.Project{ID: old.ID, Repo: hosted.Repo}
	oldOwnerPlan, err := source.store.IngestIssue(ctx, owner, oldIssue)
	if err != nil {
		t.Fatal(err)
	}
	ownerSnapshot, err := source.store.Get(ctx, "execution_plans", oldOwnerPlan.ID)
	if err != nil {
		t.Fatal(err)
	}
	priorIssue := model.Issue{Number: 20, Title: "Prior repository issue", State: "OPEN", Labels: []string{"whirlwind:ready"}}
	plan, err := source.store.IngestIssue(ctx, hosted, priorIssue)
	if err != nil {
		t.Fatal(err)
	}
	priorPlan, err := source.store.Get(ctx, "execution_plans", plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	return legacyCheckoutIssues{oldIssue: oldIssue, priorIssue: priorIssue, oldOwnerPlan: oldOwnerPlan, plan: plan, ownerSnapshot: ownerSnapshot, priorPlan: priorPlan}
}

func reopenLegacyCheckout(t *testing.T, fixture legacyCheckoutFixture) *Source {
	t.Helper()
	ctx, source, repo, old := t.Context(), fixture.source, fixture.repo, fixture.old
	// A checkout with a managed policy must ignore edits to its local policy file.
	writeBrokenProjectPolicy(t, repo)

	restarted, err := attachSource(ctx, source.store, source.path)
	if err != nil {
		t.Fatal(err)
	}
	archived, err := source.store.Get(ctx, "archived_projects", old.ID)
	if err != nil || !reflect.DeepEqual(archived.Data, old.Data) || archived.Version != old.Version {
		t.Fatalf("archive did not preserve the original registration: %+v, %v", archived, err)
	}
	return restarted
}

func assertLegacyCheckout(t *testing.T, restarted *Source, want model.Project, history []model.Record) model.Project {
	ctx := t.Context()
	t.Helper()
	rows, err := restarted.store.List(ctx, "projects")
	if err != nil || len(rows) != 1 || rows[0].ID != want.ID {
		t.Fatalf("expected one active registration: %v, %v", rows, err)
	}
	got, err := store.Decode[model.Project](rows[0])
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("wrong active registration: %+v, want %+v: %v", got, want, err)
	}
	for _, binding := range savedHealthBindings(t, restarted) {
		if binding.ProjectID != want.ID || binding.Status != "OK" {
			t.Fatalf("configured alias lost its binding: %+v", binding)
		}
	}
	after, err := restarted.store.List(ctx, "operations")
	if err != nil || !reflect.DeepEqual(after, history) {
		t.Fatalf("setup or run history changed: %v, %v", after, err)
	}
	return got
}

func assertLegacyIssueOwner(t *testing.T, fixture legacyCheckoutFixture, issues legacyCheckoutIssues) {
	t.Helper()
	ctx, source, old, hosted := t.Context(), fixture.source, fixture.old, fixture.hosted
	oldIssue, oldOwnerPlan, ownerSnapshot := issues.oldIssue, issues.oldOwnerPlan, issues.ownerSnapshot
	oldIssue.Title = "Changed upstream issue"
	held, err := source.store.IngestIssue(ctx, hosted, oldIssue)
	if err != nil || held.ID != oldOwnerPlan.ID || held.ProjectID != old.ID {
		t.Fatalf("the old registration's plan changed owners: %+v, %v", held, err)
	}
	ownerAfter, err := source.store.Get(ctx, "execution_plans", held.ID)
	if err != nil || !reflect.DeepEqual(ownerAfter, ownerSnapshot) {
		t.Fatalf("the old registration's issue history changed: %+v, %v", ownerAfter, err)
	}
}

func assertReplacementOriginIsolation(t *testing.T, restarted *Source, fixture legacyCheckoutFixture, hosted model.Project, history []model.Record, issues legacyCheckoutIssues) model.Project {
	t.Helper()
	ctx, source, repo := t.Context(), fixture.source, fixture.repo
	priorIssue, plan := issues.priorIssue, issues.plan
	const replacement = "https://github.com/example/replacement"
	projectOriginGit(t, repo, "remote", "set-url", "origin", replacement+".git")
	if err := restarted.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	hosted.Repo, hosted.RemoteURL = replacement, replacement
	current := assertLegacyCheckout(t, restarted, hosted, history)
	matches, err := source.store.IssueBoundToPlan(ctx, current.Repo, priorIssue.Number, plan.ID)
	if err != nil || matches {
		t.Fatalf("replacement origin inherited the old issue binding: %t, %v", matches, err)
	}
	incoming := priorIssue
	incoming.Title = "Replacement repository issue"
	second, err := source.store.IngestIssue(ctx, current, incoming)
	if err != nil || second.ID == plan.ID || second.ProjectID != hosted.ID {
		t.Fatalf("replacement issue did not receive its own binding: %+v, %v", second, err)
	}
	return hosted
}

func assertRestoredOriginHistory(t *testing.T, restarted *Source, fixture legacyCheckoutFixture, hosted model.Project, history []model.Record, issues legacyCheckoutIssues) {
	t.Helper()
	ctx, source, repo := t.Context(), fixture.source, fixture.repo
	priorIssue, plan, priorPlan := issues.priorIssue, issues.plan, issues.priorPlan
	projectOriginGit(t, repo, "remote", "set-url", "origin", "git@github.com:Example/Fixture-Repository.git")
	if err := restarted.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	hosted.Repo, hosted.RemoteURL = "https://github.com/Example/Fixture-Repository", "https://github.com/Example/Fixture-Repository"
	current := assertLegacyCheckout(t, restarted, hosted, history)
	returned, err := source.store.IngestIssue(ctx, current, priorIssue)
	if err != nil || returned.ID != plan.ID {
		t.Fatalf("returning to the original GitHub repository lost its legacy issue binding: %+v, %v", returned, err)
	}
	oldPlan, err := source.store.Get(ctx, "execution_plans", plan.ID)
	if err != nil || !reflect.DeepEqual(oldPlan, priorPlan) {
		t.Fatalf("prior issue history changed while origin was replaced: %+v, %v", oldPlan, err)
	}
}

func assertLegacyCheckoutReferences(t *testing.T, source *Source) {
	t.Helper()
	ctx := t.Context()
	integrity, err := source.store.DB.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := integrity.Close(); err != nil {
			t.Errorf("close foreign-key check: %v", err)
		}
	}()
	if integrity.Next() || integrity.Err() != nil {
		t.Fatalf("migration broke SQLite references: %v", integrity.Err())
	}
}

func TestSameOriginKeepsSeparateConfiguredCheckouts(t *testing.T) {
	source := editorSource(t)
	first, second := healthRepository(t), healthRepository(t)
	for _, path := range []string{first, second} {
		projectOriginGit(t, path, "remote", "add", "origin", fixtureOrigin+".git")
	}
	refreshHealthProjects(t, source, first, second)
	bindings := savedHealthBindings(t, source)
	if len(bindings) != 2 || bindings[0].ProjectID == bindings[1].ProjectID || bindings[0].Status != "OK" || bindings[1].Status != "OK" {
		t.Fatalf("distinct checkouts were conflated: %+v", bindings)
	}
}
