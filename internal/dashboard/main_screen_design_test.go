package dashboard

import (
	"os"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"whirlwind/internal/control"
	"whirlwind/internal/model"
	"whirlwind/internal/workflow"
)

const indexingTitle = "Improve indexing"
const clarificationMessage = "Agent clarification awaits answer: question"

func TestMainScreenShowsIdeasInInbox(t *testing.T) {
	d := New()
	screen := tcell.NewSimulationScreen("UTF-8")
	d.SetScreen(screen)
	t.Cleanup(screen.Fini)
	screen.SetSize(140, 45)
	d.Present(control.Snapshot{
		"projects":   {record("p", model.Project{ID: "p", Name: "Repo"})},
		"directions": {record("idea", model.Direction{ID: "idea", ProjectID: "p", Title: indexingTitle, Category: "optimize", Status: "proposed"})},
	})
	d.ForceDraw()
	view := screenText(screen)
	for _, want := range []string{" Projects ", " Agents ", " Runs ", " Inbox ", indexingTitle} {
		if !strings.Contains(view, want) {
			t.Errorf("main screen missing %q", want)
		}
	}
}

func TestRunDetailsRemainAccessibleFromRuns(t *testing.T) {
	d := New()
	d.Present(control.Snapshot{
		"projects": {record("p", model.Project{ID: "p", Name: "Repo"})},
		"sessions": {record("run", model.Attempt{ProjectID: "p", Provider: "codex", Status: "failed", Error: "connection lost"})},
	})
	d.focus = 2
	d.input(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	view, ok := d.modal.(*tview.TextView)
	if !ok || !strings.Contains(view.GetTitle(), "Run logs") || !strings.Contains(view.GetText(false), "connection lost") {
		t.Fatal("run detail is not available from Runs")
	}
}

func TestInboxLabelsIdeasWithCategory(t *testing.T) {
	d := New()
	d.Present(control.Snapshot{
		"projects":   {record("p", model.Project{ID: "p"})},
		"directions": {record("idea", model.Direction{ID: "idea", ProjectID: "p", Title: indexingTitle, Category: "optimize", Status: "proposed"})},
	})
	label, _ := d.directions.GetItemText(0)
	if !strings.Contains(label, "Ideation (Optimize) - Improve indexing") {
		t.Fatalf("unexpected inbox label: %q", label)
	}
}

func TestTaskInspectionOpensDetailsWhenInboxContainsIdeas(t *testing.T) {
	d := New()
	d.Present(control.Snapshot{
		"projects":   {record("p", model.Project{ID: "p"})},
		"directions": {record("idea", model.Direction{ID: "idea", ProjectID: "p", Title: "Idea"})},
		"tasks":      {record("task", model.Task{ID: "task", ProjectID: "p", Plan: &model.Plan{Objective: "Inspect me"}})},
	})
	d.chooseTask()
	d.plans.GetSelectedFunc()(0, "", "", 0)
	if d.modal != d.detail || !strings.Contains(d.detail.GetText(false), "Inspect me") {
		t.Fatal("task detail is not accessible")
	}
}

func TestMainScreenUsesProportionalPanelLayout(t *testing.T) {
	d := New()
	screen := tcell.NewSimulationScreen("UTF-8")
	d.SetScreen(screen)
	t.Cleanup(screen.Fini)
	screen.SetSize(140, 45)
	d.ForceDraw()
	_, _, projectWidth, projectHeight := d.projects.GetRect()
	_, _, _, agentHeight := d.agents.GetRect()
	if projectWidth < 36 || projectWidth > 42 {
		t.Errorf("left column width = %d", projectWidth)
	}
	if projectHeight < 11 || projectHeight > 16 || agentHeight < 11 || agentHeight > 16 {
		t.Errorf("panel heights = %d, %d", projectHeight, agentHeight)
	}
}

func TestClosingDirectionChooserRestoresInboxTitle(t *testing.T) {
	d := New()
	d.Present(control.Snapshot{
		"projects":   {record("p", model.Project{ID: "p"})},
		"directions": {record("idea", model.Direction{ID: "idea", ProjectID: "p", Title: "Idea"})},
	})
	d.chooseDirection()
	d.closeModal()
	if got := d.directions.GetTitle(); got != " Inbox " {
		t.Fatalf("title after close = %q", got)
	}
}

func TestEmptyInboxShowsEmptyState(t *testing.T) {
	d := New()
	screen := tcell.NewSimulationScreen("UTF-8")
	d.SetScreen(screen)
	t.Cleanup(screen.Fini)
	screen.SetSize(140, 45)
	d.Present(control.Snapshot{"projects": {record("p", model.Project{ID: "p"})}})
	d.ForceDraw()
	if !strings.Contains(screenText(screen), "No inbox items") {
		t.Fatal("empty Inbox state missing")
	}
}

func TestInboxRowsHaveNoBlankLines(t *testing.T) {
	d := New()
	screen := tcell.NewSimulationScreen("UTF-8")
	d.SetScreen(screen)
	t.Cleanup(screen.Fini)
	screen.SetSize(140, 45)
	d.Present(control.Snapshot{
		"projects":   {record("p", model.Project{ID: "p"})},
		"directions": {record("a", model.Direction{ID: "a", ProjectID: "p", Title: "First idea"}), record("b", model.Direction{ID: "b", ProjectID: "p", Title: "Second idea"})},
	})
	d.ForceDraw()
	rows := strings.Split(screenText(screen), "\n")
	first, second := -1, -1
	for i, row := range rows {
		if strings.Contains(row, "First idea") {
			first = i
		}
		if strings.Contains(row, "Second idea") {
			second = i
		}
	}
	if first < 0 || second-first != 1 {
		t.Fatalf("idea rows at %d and %d", first, second)
	}
}

func TestAgentStatusHasColoredIndicator(t *testing.T) {
	d := New()
	screen := tcell.NewSimulationScreen("UTF-8")
	d.SetScreen(screen)
	t.Cleanup(screen.Fini)
	screen.SetSize(140, 45)
	d.Present(control.Snapshot{
		"providers":       {record("agent", model.Provider{ID: "agent", Name: "Agent"})},
		"provider_status": {record("agent", workflow.ProviderStatus{ID: "agent", Status: "working"})},
	})
	d.ForceDraw()
	cells, _, _ := screen.GetContents()
	for _, cell := range cells {
		if len(cell.Runes) > 0 && cell.Runes[0] == '■' {
			foreground, _, _ := cell.Style.Decompose()
			if foreground == tcell.ColorGreen {
				return
			}
		}
	}
	t.Fatal("green agent indicator missing")
}

func TestInboxLabelsAgentQuestionsWithTheirRoles(t *testing.T) {
	for _, tc := range []struct{ role, label string }{{"review", "Reviewer"}, {"code", "Coding"}} {
		t.Run(tc.role, func(t *testing.T) {
			d := New()
			screen := tcell.NewSimulationScreen("UTF-8")
			d.SetScreen(screen)
			t.Cleanup(screen.Fini)
			screen.SetSize(140, 45)
			d.Present(control.Snapshot{
				"projects":  {record("p", model.Project{ID: "p"})},
				"tasks":     {record("task", model.Task{ID: "task", ProjectID: "p"})},
				"sessions":  {record("attempt", model.Attempt{AttemptID: "attempt", Task: "task", ProjectID: "p", Role: tc.role})},
				"questions": {record("question", model.QuestionSet{ID: "question", TaskID: "task", AttemptID: "attempt", Status: "pending"})},
				"inbox":     {record("notice", model.Notification{ID: "notice", TaskID: "task", Kind: "question", Message: clarificationMessage})},
			})
			d.ForceDraw()
			if !strings.Contains(screenText(screen), tc.label+" - "+clarificationMessage) {
				t.Fatalf("%s message missing from Inbox", tc.label)
			}
		})
	}
}

func TestSelectingInboxIdeaApprovesSelectedIdea(t *testing.T) {
	d := New()
	d.Present(control.Snapshot{
		"projects":   {record("p", model.Project{ID: "p"})},
		"directions": {record("a", model.Direction{ID: "a", ProjectID: "p", Title: "First"}), record("b", model.Direction{ID: "b", ProjectID: "p", Title: "Second"})},
	})
	d.inbox.SetCurrentItem(1)
	if d.directionID != "b" {
		t.Fatalf("selected direction = %q", d.directionID)
	}
}

func TestInboxLabelsPermissionWithItsAgentRole(t *testing.T) {
	d := New()
	screen := tcell.NewSimulationScreen("UTF-8")
	d.SetScreen(screen)
	t.Cleanup(screen.Fini)
	screen.SetSize(140, 45)
	d.Present(control.Snapshot{
		"projects":    {record("p", model.Project{ID: "p"})},
		"tasks":       {record("task", model.Task{ID: "task", ProjectID: "p"})},
		"sessions":    {record("attempt", model.Attempt{AttemptID: "attempt", Task: "task", ProjectID: "p", Role: "plan"})},
		"permissions": {record("permission", map[string]any{"id": "permission", "task": "task", "attempt_id": "attempt"})},
		"inbox":       {record("notice", model.Notification{ID: "notice", TaskID: "task", Kind: "permission", Message: "Tool permission awaits decision: permission"})},
	})
	d.ForceDraw()
	if !strings.Contains(screenText(screen), "Planner - Tool permission awaits decision: permission") {
		t.Fatal("planner permission missing from Inbox")
	}
}

func TestEnterOnInboxIdeaOpensVimDecision(t *testing.T) {
	capture := decisionVim(t, "cancel", "")
	d := New()
	dashboardScreen(t, d)
	d.Present(control.Snapshot{
		"projects":   {record("p", model.Project{ID: "p"})},
		"directions": {record("idea", model.Direction{ID: "idea", ProjectID: "p", Title: "Idea"})},
	})
	selectItem := d.inbox.GetSelectedFunc()
	if selectItem == nil {
		t.Fatal("Inbox has no selection action")
	}
	selectItem(0, "", "", 0)
	body, err := os.ReadFile(capture)
	if err != nil || !strings.Contains(string(body), "Decision: cancel") {
		t.Fatalf("Inbox did not open decision editor: %v", err)
	}

}

func TestMissingAttemptDoesNotInventAgentRole(t *testing.T) {
	d := New()
	d.Present(control.Snapshot{
		"projects":  {record("p", model.Project{ID: "p"})},
		"sessions":  {record("attempt", model.Attempt{ProjectID: "p", Role: "code"})},
		"questions": {record("question", model.QuestionSet{ID: "question", TaskID: "task"})},
		"inbox":     {record("notice", model.Notification{TaskID: "task", Kind: "question", Message: clarificationMessage})},
	})
	for i := 0; i < d.inbox.GetItemCount(); i++ {
		label, _ := d.inbox.GetItemText(i)
		if strings.Contains(label, "Coding -") {
			t.Fatal("attributed missing attempt to unrelated agent")
		}
	}
}

func TestInboxSelectionSurvivesDirectionReordering(t *testing.T) {
	d := New()
	snapshot := control.Snapshot{
		"projects":   {record("p", model.Project{ID: "p"})},
		"directions": {record("a", model.Direction{ID: "a", ProjectID: "p", Title: "First"}), record("b", model.Direction{ID: "b", ProjectID: "p", Title: "Second"})},
	}
	d.Present(snapshot)
	d.inbox.SetCurrentItem(1)
	snapshot["directions"][0], snapshot["directions"][1] = snapshot["directions"][1], snapshot["directions"][0]
	d.Present(snapshot)
	label, _ := d.inbox.GetItemText(d.inbox.GetCurrentItem())
	if !strings.Contains(label, "Second") {
		t.Fatalf("selection changed to %q", label)
	}
}

func TestMainScreenStartsWithPanelsAndKeepsStatusAtBottom(t *testing.T) {
	d := New()
	screen := tcell.NewSimulationScreen("UTF-8")
	d.SetScreen(screen)
	t.Cleanup(screen.Fini)
	screen.SetSize(100, 30)
	d.ForceDraw()
	rows := strings.Split(screenText(screen), "\n")
	if !strings.Contains(rows[0], "Projects") || !strings.Contains(rows[0], "Inbox") {
		t.Fatal("panels should start at top")
	}
	if !strings.Contains(rows[29], "quit") {
		t.Fatal("keyboard help missing at bottom")
	}
}

func TestInboxNotificationOpensAssociatedTask(t *testing.T) {
	d := New()
	d.Present(control.Snapshot{
		"projects":  {record("p", model.Project{ID: "p"})},
		"tasks":     {record("task", model.Task{ID: "task", ProjectID: "p", Plan: &model.Plan{Objective: "Review the change"}})},
		"sessions":  {record("attempt", model.Attempt{AttemptID: "attempt", Task: "task", ProjectID: "p", Role: "review"})},
		"questions": {record("question", model.QuestionSet{ID: "question", TaskID: "task", AttemptID: "attempt"})},
		"inbox":     {record("notice", model.Notification{TaskID: "task", Kind: "question", Message: clarificationMessage})},
	})
	selectItem := d.inbox.GetSelectedFunc()
	selectItem(0, "", "", 0)
	if d.modal != d.detail || !strings.Contains(d.detail.GetText(false), "Review the change") {
		t.Fatal("notification did not open task details")
	}
}

func TestDashboardInspectsSavedPlansAndPendingQuestions(t *testing.T) {
	d := New()
	screen := tcell.NewSimulationScreen("UTF-8")
	d.SetScreen(screen)
	t.Cleanup(screen.Fini)
	screen.SetSize(140, 45)
	d.Present(control.Snapshot{
		"projects":  {record("p", model.Project{ID: "p", Name: "Saved repository"})},
		"tasks":     {record("plan", model.Task{ID: "plan", ProjectID: "p", Stage: "plan_approval", PlanVersion: 2, Branch: "delivery/saved", Plan: &model.Plan{Objective: "Preserve operator choices", AcceptanceCriteria: []string{"Pending question remains answerable"}}})},
		"questions": {record("q", model.QuestionSet{TaskID: "plan", Status: "pending", Questions: []model.Question{{Text: "Which directory should receive output?"}}})},
		"sessions":  {record("a", model.Attempt{Task: "plan", Role: "plan", Provider: "cursor", ResolvedModel: "chosen-model", Status: "paused"})},
	})
	d.ForceDraw()
	if !strings.Contains(screenText(screen), "Saved repository") {
		t.Fatal("saved repository missing from main screen")
	}
	d.focus = 2
	d.input(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	d.ForceDraw()
	text := screenText(screen)
	for _, expected := range []string{"Preserve operator choices", "plan_approval", "delivery/saved", "Which directory", "cursor", "chosen-model", "paused"} {
		if !strings.Contains(text, expected) {
			t.Errorf("saved state missing %q", expected)
		}
	}
}

func TestInboxUsesRecordedSenderWithoutAttemptHistory(t *testing.T) {
	d := New()
	d.Present(control.Snapshot{
		"projects": {record("p", model.Project{ID: "p"})},
		"tasks":    {record("task", model.Task{ID: "task", ProjectID: "p"})},
		"inbox":    {record("notice", model.Notification{TaskID: "task", Kind: "question", Message: "Need clarification", Role: "review"})},
	})
	label, _ := d.inbox.GetItemText(0)
	if label != "time unknown · Reviewer - Need clarification" {
		t.Fatalf("label = %q", label)
	}
}
