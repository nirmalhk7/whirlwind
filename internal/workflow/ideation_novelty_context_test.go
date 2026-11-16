package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

const noveltySourceHash = "source-hash"

type noveltyPromptCapture struct {
	prompt string
}

func (capture *noveltyPromptCapture) Run(_ context.Context, input provider.Input, _ provider.Hooks) (provider.Output, error) {
	capture.prompt = input.Prompt
	return provider.Output{Text: `{"unique":true,"reason":"supported","duplicate_categories":[]}`}, nil
}

func TestNoveltyReviewUsesFullCapturedCandidateSourceAndIndexedPathInventory(t *testing.T) {
	engine, task, repo := testEngine(t)
	project, err := engine.Project(context.Background(), task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	const candidatePath = "src/service.ts"
	const otherPath = "src/other.ts"
	const frozenTail = "export const frozenEvidenceTail = 'FROZEN-SOURCE-TAIL';\n"
	writeDiscoveryFile(t, repo, candidatePath, strings.Repeat("export const feature = true;\n", 35)+frozenTail)
	writeDiscoveryFile(t, repo, otherPath, "export const other = true;\n")
	_, _, snapshot, _, err := engine.prepareDiscovery(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	writeDiscoveryFile(t, repo, candidatePath, "export const feature = 'changed after capture';\n")

	capture := &noveltyPromptCapture{}
	engine.Adapter = func(model.Provider) provider.Adapter { return capture }
	candidate := model.Direction{
		Category:    "expand",
		Title:       "Add a product view",
		Description: "Expose another useful view of the product.",
		Components:  []string{candidatePath, "src/new-view.tsx"},
		Evidence:    []model.EvidenceRef{{Kind: "code", Ref: candidatePath}},
	}
	_, _, _, err = engine.reviewDirectionNovelty(context.Background(), directionGenerationRequest{
		project:      project,
		snapshot:     snapshot,
		runID:        "novelty-context-test",
		budgetTaskID: "ideation:fixture",
	}, []model.Direction{candidate}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(capture.prompt, "changed after capture") {
		t.Fatal("novelty prompt read source changed after the snapshot was captured")
	}
	repository := capturedNoveltyRepositoryContext(t, capture.prompt)
	if !repository.IndexedTextPathsComplete || !containsDiscoveryPath(repository.IndexedTextPaths, otherPath) {
		t.Fatal("novelty prompt omitted the indexed path inventory")
	}
	sources := make(map[string]noveltyCandidateSource, len(repository.CandidateSources))
	for _, source := range repository.CandidateSources {
		sources[source.Path] = source
	}
	if source := sources[candidatePath]; !source.Complete || source.Content != strings.Repeat("export const feature = true;\n", 35)+frozenTail {
		t.Fatal("novelty prompt omitted complete frozen source linked by the candidate")
	}
	if !candidatePathStatus(repository.CandidatePaths, candidatePath, true) || !candidatePathStatus(repository.CandidatePaths, "src/new-view.tsx", false) {
		t.Fatal("novelty prompt omitted candidate source or component paths")
	}
	if len(capturedNoveltyRepositoryJSON(t, capture.prompt)) > noveltyRepositoryContextLimit {
		t.Fatal("novelty repository context exceeded its existing bound")
	}
}

func capturedNoveltyRepositoryContext(t *testing.T, prompt string) noveltyRepositoryContext {
	t.Helper()
	var repository noveltyRepositoryContext
	if err := json.Unmarshal([]byte(capturedNoveltyRepositoryJSON(t, prompt)), &repository); err != nil {
		t.Fatalf("novelty repository context is not valid JSON: %v", err)
	}
	return repository
}

func capturedNoveltyRepositoryJSON(t *testing.T, prompt string) string {
	t.Helper()
	const heading = "Indexed source paths and candidate-linked frozen source files (captured with the repository snapshot):\n"
	const next = "\nThe path inventory covers safe indexed text files."
	start := strings.Index(prompt, heading)
	if start < 0 {
		t.Fatal("novelty prompt omitted the frozen repository context heading")
	}
	start += len(heading)
	end := strings.Index(prompt[start:], next)
	if end < 0 {
		t.Fatal("novelty prompt omitted the repository path scope notice")
	}
	return prompt[start : start+end]
}

func candidatePathStatus(paths []noveltyCandidatePath, expected string, indexed bool) bool {
	for _, path := range paths {
		if path.Path == expected {
			return path.Indexed == indexed
		}
	}
	return false
}

func TestNoveltyRepositoryContextKeepsBoundedJSONAndMarksOmittedSource(t *testing.T) {
	const sourcePath = "src/large-service.ts"
	snapshot := discoverySnapshot{
		Files:                map[string]string{sourcePath: noveltySourceHash},
		repositorySourceText: map[string]string{sourcePath: strings.Repeat("source ", noveltyRepositoryContextLimit)},
	}
	candidate := model.Direction{Components: []string{sourcePath}, Evidence: []model.EvidenceRef{{Kind: "code", Ref: sourcePath}}}
	encoded := noveltyRepositoryContextJSON(snapshot, []model.Direction{candidate})
	if len(encoded) > noveltyRepositoryContextLimit || !json.Valid([]byte(encoded)) {
		t.Fatalf("novelty repository context is not valid bounded JSON: bytes=%d", len(encoded))
	}
	var repository noveltyRepositoryContext
	if err := json.Unmarshal([]byte(encoded), &repository); err != nil {
		t.Fatal(err)
	}
	if repository.CandidateSourcesComplete || len(repository.CandidateSources) != 1 || repository.CandidateSources[0].Complete || repository.CandidateSources[0].Content != "" {
		t.Fatalf("omitted oversized source was presented as complete evidence: %+v", repository.CandidateSources)
	}
}

func TestNoveltyRepositoryContextMarksUncapturedIndexedSourceIncomplete(t *testing.T) {
	const sourcePath = "src/service.ts"
	candidate := model.Direction{Components: []string{sourcePath}}
	encoded := noveltyRepositoryContextJSON(discoverySnapshot{Files: map[string]string{sourcePath: noveltySourceHash}}, []model.Direction{candidate})
	var repository noveltyRepositoryContext
	if err := json.Unmarshal([]byte(encoded), &repository); err != nil {
		t.Fatal(err)
	}
	if repository.CandidateSourcesComplete || len(repository.CandidateSources) != 1 || repository.CandidateSources[0].Complete {
		t.Fatalf("missing frozen source was presented as complete: %+v", repository)
	}
}

func TestNoveltyRepositoryContextBoundsCandidateSourceMetadata(t *testing.T) {
	files := map[string]string{}
	sourceText := map[string]string{}
	components := make([]string, 250)
	for n := range components {
		path := "src/" + strings.Repeat("a", 80) + string(rune('A'+n%26)) + string(rune('a'+n/26))
		components[n] = path
		files[path] = noveltySourceHash
		sourceText[path] = "source"
	}
	candidate := model.Direction{Components: components}
	encoded := noveltyRepositoryContextJSON(discoverySnapshot{Files: files, repositorySourceText: sourceText}, []model.Direction{candidate})
	if len(encoded) > noveltyRepositoryContextLimit || !json.Valid([]byte(encoded)) {
		t.Fatalf("novelty source metadata exceeded the JSON context bound: bytes=%d", len(encoded))
	}
	var repository noveltyRepositoryContext
	if err := json.Unmarshal([]byte(encoded), &repository); err != nil {
		t.Fatal(err)
	}
	if repository.CandidateSourcesComplete {
		t.Fatal("truncated candidate source metadata was presented as complete")
	}
}
