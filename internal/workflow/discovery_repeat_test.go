package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	ctxengine "whirlwind/internal/context"
	"whirlwind/internal/model"
)

const (
	discoveryPagePath    = "src/app/page.tsx"
	discoveryMapPath     = "src/components/Map.tsx"
	discoveryCatalogPath = "src/server/catalog.ts"
	discoveryDeletedPath = "src/server/deleted.ts"
	discoveryNewPath     = "src/server/new.ts"
	discoveryLongPath    = "src/sample/long.go"
)

func TestRepeatDiscoverySuppliesCiteableRepositoryContext(t *testing.T) {
	e, task, _ := testEngine(t)
	ctx := context.Background()
	project, err := e.Project(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	_, _, first, _, err := e.prepareDiscovery(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.saveDiscoverySnapshot(ctx, project.ID, first); err != nil {
		t.Fatal(err)
	}
	// A policy change warrants another run without changing repository files.
	project.PolicyHash = "updated-policy"
	prompt, _, repeat, unchanged, err := e.prepareDiscovery(ctx, project)
	if err != nil || unchanged {
		t.Fatalf("repeat discovery skipped: unchanged=%v err=%v", unchanged, err)
	}
	if len(repeat.CurrentRepository) == 0 || !strings.Contains(prompt, "max_attempts: 100") {
		t.Fatal("repeat discovery omitted existing repository source")
	}
	candidate := ideaBatch(t)[0]
	candidate.ProjectID = project.ID
	if err := validateCandidateEvidence([]model.Direction{candidate}, repeat); err != nil {
		t.Fatalf("existing code cannot be cited on repeat discovery: %v", err)
	}
	if err := e.saveDiscoverySnapshot(ctx, project.ID, repeat); err != nil {
		t.Fatal(err)
	}
	_, _, _, unchanged, err = e.prepareDiscovery(ctx, project)
	if err != nil || !unchanged {
		t.Fatalf("identical repeat should skip inference: unchanged=%v err=%v", unchanged, err)
	}
}

func TestPrepareDiscoveryProvidesCompleteFrozenSourceBeyondExcerpt(t *testing.T) {
	e, task, _ := testEngine(t)
	project, err := e.Project(context.Background(), task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	const tail = "GENERATOR_SOURCE_TAIL_MARKER"
	content := "package sample\n" + strings.Repeat("// captured source line\n", 80) + "// " + tail + "\n"
	writeDiscoveryFile(t, project.Path, discoveryLongPath, content)
	prompt, _, snapshot, _, err := e.prepareDiscovery(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, tail) {
		t.Fatal("generation prompt omitted source beyond the 720-byte repository excerpt")
	}
	if snapshot.repositorySourceText[discoveryLongPath] != content {
		t.Fatal("generation evidence did not retain exact captured source bytes")
	}
	if !hasDiscoveryRevision(snapshot.Evidence, discoveryLongPath, "worktree:"+snapshot.Commit+":"+snapshot.Files[discoveryLongPath]) {
		t.Fatal("complete generator source does not have its exact worktree evidence revision")
	}
}

func TestIdeationRepositoryContextSkipsOversizedFileAndKeepsLaterSource(t *testing.T) {
	const useful = "package useful\nconst Captured = \"later file included\"\n"
	const oversizedPath = "a/oversized.go"
	const usefulPath = "b/useful.go"
	index := ctxengine.Index{Commit: "revision", Files: map[string]ctxengine.Entry{
		oversizedPath: {Text: strings.Repeat("x", ideationRepositoryContextLimit+1)},
		usefulPath:    {Text: useful},
	}}
	snapshot := discoverySnapshot{
		Commit:               "revision",
		Files:                map[string]string{oversizedPath: "hash-large", usefulPath: "hash-small"},
		repositorySourceText: map[string]string{oversizedPath: strings.Repeat("x", ideationRepositoryContextLimit+1), usefulPath: useful},
	}
	encoded := ideationRepositoryContextJSON(&snapshot, index, ideationRepositoryContextLimit)
	if len(encoded) > ideationRepositoryContextLimit {
		t.Fatalf("repository prompt context exceeded byte cap: got %d bytes", len(encoded))
	}
	var got ideationRepositoryContext
	if err := json.Unmarshal([]byte(encoded), &got); err != nil {
		t.Fatalf("repository context is not valid JSON: %v", err)
	}
	if got.SourceCoverageComplete {
		t.Fatal("omitted oversized file was reported as complete source coverage")
	}
	if len(got.IndexedPaths) != 2 || !got.IndexedPathsComplete {
		t.Fatalf("complete path inventory was lost: %+v", got)
	}
	if len(got.Sources) != 1 || got.Sources[0].Path != usefulPath || !got.Sources[0].Complete || got.Sources[0].Content != useful || got.Sources[0].Revision != "revision" {
		t.Fatalf("later complete source was crowded out or changed: %+v", got.Sources)
	}
	if revision, ok := discoveryCodeEvidenceRevision(snapshot.Evidence, usefulPath); !ok || revision != "revision" {
		t.Fatalf("source revision is not an authoritative citeable evidence reference: %q %v", revision, ok)
	}
}

func TestRepeatDiscoveryUsesLeastExploredRepositoryFiles(t *testing.T) {
	const unexploredSource = "quiet.go"
	index := ctxengine.Index{Commit: "revision", Files: map[string]ctxengine.Entry{}}
	previous := discoverySnapshot{Files: map[string]string{}}
	for _, path := range []string{"busy.go", unexploredSource} {
		index.Files[path] = ctxengine.Entry{Text: "package fixture"}
		previous.Files[path] = model.Fingerprint([]byte("package fixture"))
	}
	snapshot := discoverySnapshot{Files: map[string]string{}}
	changed, paths := indexedDiscoveryChanges(index, previous, &snapshot)
	areas := rotatingDiscoveryAreas(paths, map[string]int{"busy.go": 5}, 1)
	entries := addDiscoveryRepositoryContext(changed, areas, index, &snapshot)
	if len(entries) != 1 || entries[0]["path"] != unexploredSource {
		t.Fatalf("repeat context did not select least explored source: %+v", entries)
	}
	if len(snapshot.Evidence) != 1 || snapshot.Evidence[0].Kind != "code" || snapshot.Evidence[0].Ref != unexploredSource || snapshot.Evidence[0].Revision != "revision" {
		t.Fatalf("repeat source lacks pinned evidence: %+v", snapshot.Evidence)
	}
}

func TestRepeatDiscoverySamplesGuidanceAndSourceAcrossDirectories(t *testing.T) {
	paths := []string{
		"AGENTS.md",
		"PLAN.md",
		"public/globe.svg",
		"src/__tests__/AreaSearch.test.tsx",
		discoveryPagePath,
		discoveryMapPath,
		discoveryCatalogPath,
		"src/utils/geometry.ts",
	}
	got := rotatingDiscoveryAreas(paths, map[string]int{}, 5)
	if !containsDiscoveryPath(got, "AGENTS.md") || !containsDiscoveryPath(got, "PLAN.md") {
		t.Fatalf("repository guidance was omitted from the sample: %v", got)
	}
	sourceDirs := map[string]bool{}
	for _, path := range got {
		if strings.HasPrefix(path, "src/") {
			sourceDirs[filepath.Dir(path)] = true
		}
	}
	if len(sourceDirs) < 3 {
		t.Fatalf("sample did not cover executable source across repository areas: %v", got)
	}
}

func TestRepeatDiscoverySamplesFullIndexAfterManyChangedDocuments(t *testing.T) {
	index := ctxengine.Index{Commit: "revision", Files: map[string]ctxengine.Entry{}}
	previous := discoverySnapshot{Files: map[string]string{}}
	for n := 0; n < 30; n++ {
		path := fmt.Sprintf("docs/changed-%02d.md", n)
		index.Files[path] = ctxengine.Entry{Path: path, Text: "current documentation change"}
	}
	for path, text := range map[string]string{
		discoveryPagePath:       "export const page = true;",
		discoveryMapPath:        "export function Map() { return null; }",
		discoveryCatalogPath:    "export const catalog = true;",
		"src/utils/geometry.ts": "export const geometry = true;",
	} {
		index.Files[path] = ctxengine.Entry{Path: path, Text: text}
	}
	snapshot := discoverySnapshot{Files: map[string]string{}}
	changed, paths := indexedDiscoveryChanges(index, previous, &snapshot)
	areas := rotatingDiscoveryAreas(paths, nil, 12)
	repository := addDiscoveryRepositoryContext(changed, areas, index, &snapshot)
	seen := map[string]bool{}
	for _, item := range repository {
		if path, ok := item["path"].(string); ok {
			seen[path] = true
		}
	}
	for _, path := range []string{discoveryPagePath, discoveryMapPath, discoveryCatalogPath} {
		if !seen[path] {
			t.Errorf("full-index source %q was hidden by the changed-document prefix: %v", path, repository)
		}
	}
	if len(repository) > 12 {
		t.Fatalf("repository context exceeded its bounded sample: %d", len(repository))
	}
	if len(snapshot.Evidence) != len(index.Files) {
		t.Fatalf("changed source evidence was lost while sampling: got %d want %d", len(snapshot.Evidence), len(index.Files))
	}
}

func TestPrepareDiscoveryCapturesCurrentWorktreeAndInvalidatesSavedSnapshot(t *testing.T) {
	e, task, _ := testEngine(t)
	project, err := e.Project(context.Background(), task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	seedDiscoveryFixture(t, project.Path)
	_, before, _ := prepareDiscoverySnapshot(t, e, project)
	saveDiscoveryFixture(t, e, project, before)
	editDiscoveryFixture(t, project.Path)
	prompt, current, unchanged := prepareDiscoverySnapshot(t, e, project)
	assertCurrentDiscovery(t, prompt, current, unchanged)
	saveDiscoveryFixture(t, e, project, current)
	assertDiscoveryUnchanged(t, e, project)
	assertLaterWorktreeEditInvalidatesDiscovery(t, e, project, current)
}

func saveDiscoveryFixture(t *testing.T, e *Engine, project model.Project, snapshot discoverySnapshot) {
	t.Helper()
	if err := e.saveDiscoverySnapshot(context.Background(), project.ID, snapshot); err != nil {
		t.Fatal(err)
	}
}

func assertLaterWorktreeEditInvalidatesDiscovery(t *testing.T, e *Engine, project model.Project, current discoverySnapshot) {
	t.Helper()
	frozenRepository := jsonText(current.CurrentRepository)
	frozenFileHash := current.Files[discoveryPagePath]
	writeDiscoveryFile(t, project.Path, discoveryPagePath, "export const pageState = 'newer-worktree';\n")
	assertDiscoverySnapshotFrozen(t, current, frozenRepository, frozenFileHash)
	_, _, next, unchanged, err := e.prepareDiscovery(context.Background(), project)
	if err != nil || unchanged || next.Fingerprint == current.Fingerprint {
		t.Fatalf("new working-tree content did not invalidate fingerprint: unchanged=%v err=%v", unchanged, err)
	}
}

func seedDiscoveryFixture(t *testing.T, repo string) {
	t.Helper()
	writeDiscoveryFile(t, repo, discoveryPagePath, "export const pageState = 'head';\n")
	writeDiscoveryFile(t, repo, discoveryDeletedPath, "export const removed = true;\n")
	commitDiscoveryFixture(t, repo)
}

func editDiscoveryFixture(t *testing.T, repo string) {
	t.Helper()
	writeDiscoveryFile(t, repo, discoveryPagePath, "export const pageState = 'current-worktree';\n")
	if err := os.Remove(filepath.Join(repo, filepath.FromSlash(discoveryDeletedPath))); err != nil {
		t.Fatal(err)
	}
	writeDiscoveryFile(t, repo, discoveryNewPath, "export const newService = true;\n")
}

func writeDiscoveryFile(t *testing.T, repo, path, content string) {
	t.Helper()
	absolute := filepath.Join(repo, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(absolute), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func prepareDiscoverySnapshot(t *testing.T, e *Engine, project model.Project) (string, discoverySnapshot, bool) {
	t.Helper()
	prompt, _, snapshot, unchanged, err := e.prepareDiscovery(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	return prompt, snapshot, unchanged
}

func assertCurrentDiscovery(t *testing.T, prompt string, snapshot discoverySnapshot, unchanged bool) {
	t.Helper()
	if unchanged {
		t.Fatal("working-tree edits were treated as an unchanged discovery snapshot")
	}
	if !strings.Contains(prompt, "current-worktree") || !strings.Contains(prompt, "newService") {
		t.Fatal("ideation prompt omitted modified or untracked current source")
	}
	if !hasDiscoveryRevision(snapshot.Evidence, discoveryPagePath, "worktree:"+snapshot.Commit+":"+snapshot.Files[discoveryPagePath]) {
		t.Fatal("changed source evidence does not identify the captured working-tree content")
	}
	if _, exists := snapshot.Files[discoveryDeletedPath]; exists {
		t.Fatal("deleted tracked source remained in the discovery fingerprint")
	}
	if !hasDiscoveryEvidence(snapshot.Evidence, discoveryDeletedPath, "worktree-deleted") {
		t.Fatalf("deleted source lacks a working-tree evidence revision: %+v", snapshot.Evidence)
	}
}

func assertDiscoveryUnchanged(t *testing.T, e *Engine, project model.Project) {
	t.Helper()
	_, _, _, unchanged, err := e.prepareDiscovery(context.Background(), project)
	if err != nil || !unchanged {
		t.Fatalf("unchanged current snapshot should be reusable: unchanged=%v err=%v", unchanged, err)
	}
}

func assertDiscoverySnapshotFrozen(t *testing.T, snapshot discoverySnapshot, repository, fileHash string) {
	t.Helper()
	if jsonText(snapshot.CurrentRepository) != repository || snapshot.Files[discoveryPagePath] != fileHash || !strings.Contains(repository, "current-worktree") || strings.Contains(repository, "newer-worktree") {
		t.Fatal("captured prompt source or fingerprint changed after the repository file changed")
	}
}

func commitDiscoveryFixture(t *testing.T, repo string) {
	t.Helper()
	command := exec.Command("git", "-C", repo, "add", "src")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git add discovery fixture: %s: %v", output, err)
	}
	command = exec.Command("git", "-C", repo, "commit", "-m", "discovery source fixture")
	command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git commit discovery fixture: %s: %v", output, err)
	}
}

func containsDiscoveryPath(paths []string, expected string) bool {
	for _, path := range paths {
		if path == expected {
			return true
		}
	}
	return false
}

func hasDiscoveryEvidence(evidence []model.EvidenceRef, path, revisionPrefix string) bool {
	for _, ref := range evidence {
		if ref.Kind == "code" && ref.Ref == path && strings.HasPrefix(ref.Revision, revisionPrefix) {
			return true
		}
	}
	return false
}

func hasDiscoveryRevision(evidence []model.EvidenceRef, path, revision string) bool {
	for _, ref := range evidence {
		if ref.Kind == "code" && ref.Ref == path && ref.Revision == revision {
			return true
		}
	}
	return false
}
