package workflow

import (
	"bytes"
	"context"
	"os"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func TestParityAutofixArtifactIDsCannotExpandCodingScope(t *testing.T) {
	e, plan, project, _, _, patch := approvedAutofixEvidenceFixture(t)
	ctx := context.Background()
	if err := e.Store.Put(ctx, "verification", "foreign-run", map[string]string{"plan_id": "another-plan"}, 0); err != nil {
		t.Fatal(err)
	}
	foreign, err := e.Store.CaptureResource(ctx, store.ArtifactOwner{Kind: "verification", ID: "foreign-run"}, "autofix_patch:fix", "application/octet-stream", patch, store.CaptureOptions{Limit: store.PatchPayloadLimit})
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"foreign artifact", "stale version", "other candidate", "wrong project"} {
		t.Run(change, func(t *testing.T) {
			input := plan
			input.Handoff.AutofixProposals = append([]model.AutofixHandoff{}, plan.Handoff.AutofixProposals...)
			actorProject := project
			switch change {
			case "foreign artifact":
				input.Handoff.AutofixProposals[0].ArtifactID = foreign.ID
			case "stale version":
				input.Handoff.AutofixProposals[0].PlanVersion++
			case "other candidate":
				input.Handoff.AutofixProposals[0].CandidateCommit = plan.Base
			case "wrong project":
				actorProject.ID = "another-project"
			}
			run := agentRun{Engine: e, ctx: ctx, task: input, project: actorProject, input: provider.Input{Role: "code", CWD: project.Path}}
			if err := run.prepareAutofixHandoff(); err == nil || len(run.autofixReadRoots) != 0 {
				t.Fatalf("%s expanded Coding read scope: roots=%v err=%v", change, run.autofixReadRoots, err)
			}
		})
	}
	input := plan
	input.Handoff.AutofixProposals = append([]model.AutofixHandoff{}, plan.Handoff.AutofixProposals...)
	input.Handoff.AutofixProposals[0].Path = "/private/etc/hosts"
	run := agentRun{Engine: e, ctx: ctx, task: input, project: project, input: provider.Input{Role: "code", CWD: project.Path}}
	if err := run.prepareAutofixHandoff(); err != nil {
		t.Fatal(err)
	}
	path := run.task.Handoff.AutofixProposals[0].Path
	content, err := os.ReadFile(path)
	if err != nil || path == "/private/etc/hosts" || !bytes.Equal(content, patch) || len(run.autofixReadRoots) != 1 || run.autofixReadRoots[0] != path {
		t.Fatalf("untrusted handoff path selected filesystem authority: path=%s roots=%v err=%v", path, run.autofixReadRoots, err)
	}
}
