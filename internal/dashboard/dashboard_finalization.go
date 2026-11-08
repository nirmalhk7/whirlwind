package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"
	"whirlwind/internal/control"
	"whirlwind/internal/model"
	"whirlwind/internal/store"

	"github.com/rivo/tview"
)

type finalizationReport struct {
	Task             model.Task           `json:"task"`
	Review           model.Review         `json:"review"`
	Adherence        model.Adherence      `json:"adherence"`
	ApprovedPlanHash string               `json:"approved_plan_hash"`
	PolicyHash       string               `json:"policy_hash"`
	Diff             finalizationDiffPage `json:"diff"`
	Accepted         bool                 `json:"accepted"`
}

type finalizationDiffPage struct {
	Content    string `json:"content"`
	Offset     int64  `json:"offset"`
	NextOffset int64  `json:"next_offset"`
	HasMore    bool   `json:"has_more"`
}

type finalizationApprovalPayload struct {
	PlanVersion      int    `json:"plan_version"`
	ApprovedPlanHash string `json:"approved_plan_hash"`
	ReviewedCommit   string `json:"reviewed_commit"`
	ReviewID         string `json:"review_id"`
	VerificationID   string `json:"verification_id"`
	PolicyHash       string `json:"policy_hash"`
}

func (d *Dashboard) loadFinalizationReport(plan model.Task, version int, diffOffset int64, visitedOffsets []int64) {
	d.status.SetText("Whirlwind | loading accepted review, exact candidate diff, and adherence report…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		response, err := d.commands(ctx, control.Request{RequestID: store.ID(), Operation: "finalization.inspect", PlanID: plan.ID, ExpectedVersion: version, DiffOffset: diffOffset})
		d.QueueUpdateDraw(func() {
			if !d.planStillCurrent(plan.ID, version, "") {
				return
			}
			if err != nil {
				d.status.SetText("Whirlwind | finalization report unavailable: " + tview.Escape(err.Error()))
				return
			}
			var report finalizationReport
			if json.Unmarshal(response, &report) != nil {
				d.status.SetText("Whirlwind | finalization report invalid")
				return
			}
			if plan.Stage == "approval" {
				d.showFinalizationApproval(plan, version, report, visitedOffsets)
				return
			}
			pretty, _ := json.MarshalIndent(report, "", "  ")
			d.detail.SetText(string(pretty))
		})
	}()
}

func (d *Dashboard) showFinalizationApproval(plan model.Task, version int, report finalizationReport, visitedOffsets []int64) {
	policyHash, ok := d.projectPolicyHash(plan.ProjectID)
	request, err := finalizationApprovalRequest(plan, version, policyHash, report)
	if !ok || err != nil || !d.approvalPlanStillCurrent(plan, version, policyHash) {
		d.status.SetText("Whirlwind | critical candidate changed; reload the plan and report before local finalization")
		return
	}
	form := tview.NewForm()
	form.AddTextView("Exact accepted review report", finalizationReportText(report), 0, 12, false, true)
	pageLabel := fmt.Sprintf("Exact reviewed candidate diff · byte %d onward · page %d", report.Diff.Offset, len(visitedOffsets))
	form.AddTextView(pageLabel, report.Diff.Content, 0, 18, false, true)
	if len(visitedOffsets) > 1 {
		previous := append([]int64(nil), visitedOffsets[:len(visitedOffsets)-1]...)
		previousOffset := previous[len(previous)-1]
		form.AddButton("Previous diff page", func() { d.loadFinalizationReport(plan, version, previousOffset, previous) })
	}
	if report.Diff.HasMore {
		next := append(append([]int64(nil), visitedOffsets...), report.Diff.NextOffset)
		form.AddButton("Next diff page", func() { d.loadFinalizationReport(plan, version, report.Diff.NextOffset, next) })
	} else {
		form.AddButton("Approve local finalization", func() {
			if !d.approvalPlanStillCurrent(plan, version, policyHash) {
				d.closeModal()
				d.status.SetText("Whirlwind | critical candidate changed; approval was not sent")
				return
			}
			d.closeModal()
			d.submit(request)
		})
	}
	d.showForm(form)
}

func finalizationApprovalRequest(plan model.Task, version int, policyHash string, report finalizationReport) (control.Request, error) {
	if !report.Accepted || !report.Adherence.Accepted || !reflect.DeepEqual(report.Review.Adherence, &report.Adherence) || !reviewAdherenceMatchesTask(report.Review, plan, policyHash) || !reviewAdherenceMatchesTask(report.Review, report.Task, report.PolicyHash) {
		return control.Request{}, fmt.Errorf("current accepted review report does not match the selected candidate")
	}
	if report.Task.ID != plan.ID || report.Task.Stage != "approval" || report.Task.Risk != "critical" || report.Task.PlanVersion != plan.PlanVersion || report.Task.ReviewedCommit != plan.ReviewedCommit || report.Task.ReviewID != plan.ReviewID || report.Task.VerificationID != plan.VerificationID || report.Task.PlanDocument == nil {
		return control.Request{}, fmt.Errorf("critical approval report changed since it was selected")
	}
	approvedHash := report.Task.PlanDocument.ApprovedHash
	if approvedHash == "" || report.ApprovedPlanHash != approvedHash || report.PolicyHash != policyHash || report.Diff.Content == "" || report.Diff.HasMore {
		return control.Request{}, fmt.Errorf("critical approval report is missing the approved plan, policy, or exact diff")
	}
	payload := finalizationApprovalPayload{PlanVersion: report.Task.PlanVersion, ApprovedPlanHash: approvedHash, ReviewedCommit: report.Task.ReviewedCommit, ReviewID: report.Task.ReviewID, VerificationID: report.Task.VerificationID, PolicyHash: report.PolicyHash}
	body, err := json.Marshal(payload)
	if err != nil {
		return control.Request{}, err
	}
	return control.Request{Operation: "finalization.approve", PlanID: report.Task.ID, ExpectedVersion: version, IdempotencyKey: store.ID(), Payload: body}, nil
}

func (d *Dashboard) approvalPlanStillCurrent(plan model.Task, version int, policyHash string) bool {
	if !d.planStillCurrent(plan.ID, version, "") || d.projectPolicyHashOrEmpty(plan.ProjectID) != policyHash {
		return false
	}
	_, current, ok := d.planRecord(plan.ID)
	return ok && current.Stage == "approval" && current.PlanVersion == plan.PlanVersion && current.ReviewedCommit == plan.ReviewedCommit && current.ReviewID == plan.ReviewID && current.VerificationID == plan.VerificationID && dashboardApprovedPlanHash(current) == dashboardApprovedPlanHash(plan)
}

func dashboardApprovedPlanHash(task model.Task) string {
	if task.PlanDocument == nil {
		return ""
	}
	return task.PlanDocument.ApprovedHash
}

func (d *Dashboard) projectPolicyHashOrEmpty(projectID string) string {
	value, _ := d.projectPolicyHash(projectID)
	return value
}

func finalizationReportText(report finalizationReport) string {
	text, _ := json.MarshalIndent(struct {
		Approval finalizationApprovalPayload `json:"approval_candidate"`
		Review   model.Review                `json:"review"`
		Report   model.Adherence             `json:"computed_adherence"`
	}{Approval: finalizationApprovalPayload{PlanVersion: report.Task.PlanVersion, ApprovedPlanHash: report.ApprovedPlanHash, ReviewedCommit: report.Task.ReviewedCommit, ReviewID: report.Task.ReviewID, VerificationID: report.Task.VerificationID, PolicyHash: report.PolicyHash}, Review: report.Review, Report: report.Adherence}, "", "  ")
	var view strings.Builder
	fmt.Fprintf(&view, "Approve local finalization only. No pull request or branch push.\nComputed adherence: %g%%\nUnmet requirement IDs:\n", report.Adherence.Score)
	unmet := 0
	for _, result := range report.Adherence.Results {
		if result.Status == "pass" {
			continue
		}
		fmt.Fprintf(&view, "- %s\n", result.ID)
		unmet++
	}
	if unmet == 0 {
		view.WriteString("- none\n")
	}
	view.WriteByte('\n')
	view.Write(text)
	return view.String()
}
