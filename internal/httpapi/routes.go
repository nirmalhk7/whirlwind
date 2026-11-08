package httpapi

import (
	"net/http"
	"strings"
)

type apiRequest struct {
	*Server
	w                http.ResponseWriter
	r                *http.Request
	body             []byte
	kind, id, action string
}

type apiError struct {
	status        int
	code, message string
}

func (e *apiError) Error() string { return e.message }

func parseRequest(s *Server, w http.ResponseWriter, r *http.Request, body []byte) (apiRequest, error) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/"), "/")
	q := apiRequest{Server: s, w: w, r: r, body: body, kind: parts[0]}
	if len(parts) > 3 {
		return q, &apiError{404, "route_not_found", "Unsupported route"}
	}
	if len(parts) > 1 {
		q.id = parts[1]
	}
	if len(parts) > 2 {
		q.action = parts[2]
	}
	return q, nil
}

type routeRule struct {
	method, kind, action string
	collection           bool
	handle               func() error
}

func (rule routeRule) matches(q apiRequest) bool {
	if rule.method != q.r.Method || rule.kind != q.kind {
		return false
	}
	if rule.collection && q.id != "" {
		return false
	}
	switch rule.action {
	case "*":
		return true
	case "+":
		return q.action != ""
	default:
		return rule.action == q.action
	}
}
func (q apiRequest) dispatch() error {
	rules := []routeRule{
		{"GET", "tasks", "plan-document", false, q.localPlanDocument},
		{"POST", "tasks", "plan-review", false, q.reviewLocalPlan},
		{"GET", "providers", "integrations", false, q.integrationInventory},
		{"POST", "providers", "integrations", false, q.integrationInventory},
		{"POST", "tasks", "budget-reconcile", false, q.reconcileTaskBudget},
		{"GET", "evaluation", "", true, q.evaluationReport},
		{"POST", "tasks", "outcome", false, q.taskOutcome},
		{"GET", "tasks", "budget", false, q.taskBudget},
		{"PATCH", "tasks", "budget", false, q.taskBudget},
		{"GET", "tasks", "usage", false, q.taskUsage},
		{"GET", "sessions", "events", false, q.attemptEvents},

		{"GET", "sessions", "evidence", false, q.linkedEvidence},
		{"GET", "artifacts", "", false, q.artifact},
		{"GET", "artifacts", "content", false, q.artifact},
		{"GET", "health", "*", false, q.health},
		{"GET", "overview", "*", false, q.overview},
		{"GET", "provider-status", "", true, q.providerStatus},
		{"GET", "directories", "", true, q.directories},
		{"GET", "settings", "*", true, q.getSettings},
		{"GET", "agents", "", false, q.agents},
		{"PATCH", "agents", "", false, q.agents},
		{"PATCH", "settings", "*", true, q.patchSettings},
		{"GET", "settings", "", false, q.settingsFile},
		{"PATCH", "settings", "", false, q.settingsFile},
		{"POST", "projects", "*", true, q.createProject},
		{"DELETE", "projects", "", false, q.deleteProject},
		{"POST", "projects", "pause", false, q.projectActivity},
		{"POST", "projects", "resume", false, q.projectActivity},
		{"POST", "providers", "*", true, q.createProvider},
		{"POST", "providers", "test", false, q.testProvider},
		{"POST", "providers", "models", false, q.providerModels},
		{"POST", "inbox", "", false, q.clearInbox},
		{"GET", "runners", "", false, q.runnerStatus},
		{"POST", "runners", "", false, q.stopRunners},
		{"POST", "projects", "ideation", false, q.ideate},
		{"POST", "projects", "policy", false, q.confirmPolicy},
		{"POST", "projects", "embedding-evaluation", false, q.evaluateEmbeddings},
		{"DELETE", "directions", "group", false, q.deleteDirectionGroup},
		{"POST", "directions", "select", false, q.selectDirection},
		{"POST", "directions", "deselect", false, q.deselectDirection},
		{"POST", "directions", "dismiss", false, q.candidateLifecycle},
		{"POST", "directions", "defer", false, q.candidateLifecycle},
		{"POST", "directions", "reopen", false, q.candidateLifecycle},
		{"POST", "directions", "reconsider", false, q.candidateLifecycle},
		{"POST", "tasks", "+", false, q.taskAction},
		{"POST", "questions", "answers", false, q.answerQuestions},
		{"POST", "permissions", "decisions", false, q.decidePermission},
	}
	for _, rule := range rules {
		if rule.matches(q) {
			return rule.handle()
		}
	}
	return q.resource()
}
