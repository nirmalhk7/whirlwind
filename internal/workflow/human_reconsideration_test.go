package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

type reconsiderationPromptCapture struct {
	fixture           *ideationFixture
	generationPrompts []string
}

func (a *reconsiderationPromptCapture) Run(ctx context.Context, input provider.Input, hooks provider.Hooks) (provider.Output, error) {
	if strings.Contains(input.Prompt, "Candidate ideas:") {
		return a.fixture.Run(ctx, input, hooks)
	}
	a.generationPrompts = append(a.generationPrompts, input.Prompt)
	return a.fixture.Run(ctx, input, hooks)
}

func TestHumanReconsiderationFlowsThroughIdeationOnce(t *testing.T) {
	prepared := prepareNoveltyTest(t, []string{`{"unique":true}`})
	ctx := context.Background()
	old := prepared.fixture.batch[0]
	old.ID = "rejected-for-reconsideration"
	old.ProjectID = prepared.project.ID
	old.Status = "rejected"
	old.RejectionReason = "The earlier change lacked current evidence"
	if err := prepared.engine.Store.Put(ctx, "directions", old.ID, old, 0); err != nil {
		t.Fatal(err)
	}
	oldRecord, err := prepared.engine.Store.Get(ctx, "directions", old.ID)
	if err != nil {
		t.Fatal(err)
	}
	prompt, _, snapshot, _, err := prepared.engine.prepareDiscovery(ctx, prepared.project)
	if err != nil {
		t.Fatal(err)
	}
	if prompt == "" {
		t.Fatal("expected initial repository discovery prompt")
	}
	if err := prepared.engine.saveDiscoverySnapshot(ctx, prepared.project.ID, snapshot); err != nil {
		t.Fatal(err)
	}
	const humanReason = "The current team explicitly wants this reconsidered despite unchanged evidence"
	request := model.ProposalReconsideration{
		ID:                        "human-reconsideration-request",
		ProjectID:                 prepared.project.ID,
		PredecessorID:             old.ID,
		Reason:                    humanReason,
		Status:                    "pending",
		RequestedDirectionVersion: oldRecord.Version + 1,
	}
	if err := prepared.engine.Store.Put(ctx, "proposal_reconsiderations", request.ID, request, 0); err != nil {
		t.Fatal(err)
	}
	old.Status = "reconsider_requested"
	old.Selected = false
	if err := prepared.engine.Store.Put(ctx, "directions", old.ID, old, oldRecord.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := prepared.engine.Store.PendingProposalReconsideration(ctx, prepared.project.ID, old.ID, humanReason); err != nil {
		t.Fatalf("host request was not active before ideation: %v", err)
	}

	prepared.fixture.batch[0].PredecessorID = old.ID
	prepared.fixture.batch[0].RevisitReason = humanReason
	prepared.fixture.batch[0].Status = "rejected"
	prepared.fixture.batch[0].Selected = true
	capture := &reconsiderationPromptCapture{fixture: prepared.fixture}
	prepared.engine.Adapter = func(model.Provider) provider.Adapter { return capture }

	err = prepared.engine.Ideate(ctx, prepared.project, "2026-11-07")
	if err != nil {
		rows, _ := prepared.engine.Store.List(ctx, "directions")
		pending, pendingErr := prepared.engine.Store.PendingProposalReconsideration(ctx, prepared.project.ID, old.ID, humanReason)
		hostRecord, hostErr := prepared.engine.Store.Get(ctx, "proposal_reconsiderations", request.ID)
		t.Fatalf("same-evidence human reconsideration did not complete: %v; directions=%+v pending=%+v pending_error=%v host=%+v host_error=%v", err, rows, pending, pendingErr, hostRecord, hostErr)
	}
	if len(capture.generationPrompts) != 1 || !strings.Contains(capture.generationPrompts[0], humanReason) || !strings.Contains(capture.generationPrompts[0], request.ID) {
		t.Fatalf("generator did not receive bounded trusted human request context: prompts=%q", capture.generationPrompts)
	}
	if len(prepared.fixture.reviewPrompts) != 1 || !strings.Contains(prepared.fixture.reviewPrompts[0], humanReason) || !strings.Contains(prepared.fixture.reviewPrompts[0], request.ID) {
		t.Fatalf("novelty evaluator did not receive trusted human request context: prompts=%q", prepared.fixture.reviewPrompts)
	}

	rows, err := prepared.engine.Store.List(ctx, "directions")
	if err != nil {
		t.Fatal(err)
	}
	var published model.Direction
	count := 0
	for _, row := range rows {
		direction, decodeErr := store.Decode[model.Direction](row)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if direction.PredecessorID == old.ID {
			published = direction
			count++
		}
	}
	if count != 1 || published.RevisitReason != humanReason || published.Selected || published.Status != "proposed" {
		t.Fatalf("human reconsideration did not publish exactly one unselected linked proposal: count=%d proposal=%+v", count, published)
	}
	requestRecord, err := prepared.engine.Store.Get(ctx, "proposal_reconsiderations", request.ID)
	if err != nil {
		t.Fatal(err)
	}
	consumed, err := store.Decode[model.ProposalReconsideration](requestRecord)
	if err != nil || consumed.Status != "consumed" || consumed.CandidateID != published.ID {
		t.Fatalf("human authorization was not consumed by its one publication: %+v err=%v", consumed, err)
	}

	if err := prepared.engine.Store.SaveDirections(ctx, prepared.project.ID, "2026-11-07", []model.Direction{published}); err != nil {
		t.Fatalf("ordinary selection/save path could not update the published revisit: %v", err)
	}
	var uniqueness struct {
		Status string `json:"status"`
	}
	proposal, err := prepared.engine.Store.ProposalByFingerprint(ctx, prepared.project.ID, published.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(proposal)
	if err := json.Unmarshal(data, &uniqueness); err != nil || uniqueness.Status != "proposed" {
		t.Fatalf("published fingerprint owner was not preserved after direction update: %+v err=%v", uniqueness, err)
	}
}
