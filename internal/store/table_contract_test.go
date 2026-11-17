package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"whirlwind/internal/model"
)

// These queries catch missing/renamed public SQL columns and stale projections.
func TestRequestedSQLProjectsPersistWorkflowWrites(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	project := model.Project{ID: "project_" + uuid.NewString(), Name: "Billing", Path: "/work/billing"}
	if err := s.Put(ctx, "projects", project.ID, project, 0); err != nil {
		t.Fatal(err)
	}
	var name, path, status string
	if err := s.DB.QueryRow("SELECT project_name,project_path,status FROM projects WHERE project_id=?", project.ID).Scan(&name, &path, &status); err != nil {
		t.Fatal(err)
	}
	if name != "Billing" || path != "/work/billing" || status != "draft" {
		t.Fatalf("project: %q %q %q", name, path, status)
	}
}

func TestRequestedSQLIdeasPersistWorkflowWrites(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	idea := parityIdea(t, s)
	if err := s.Put(ctx, "ideas", "i", idea, 0); err != nil {
		t.Fatal(err)
	}
	var what, helps, where, why, timestamp, status string
	if err := s.DB.QueryRow(`SELECT what,how,"where",why,timestamp,status FROM ideas WHERE idea_id='i'`).Scan(&what, &helps, &where, &why, &timestamp, &status); err != nil {
		t.Fatal(err)
	}
	if what != "Export invoices" || helps != "Export one month together" || where != "billing" || why != "Accounting needs invoices" || status != "draft" {
		t.Fatalf("idea: %q %q %q %q %q", what, helps, where, why, status)
	}
	if _, err := time.Parse(time.RFC3339Nano, timestamp); err != nil {
		t.Fatal(err)
	}
}

func TestRequestedSQLPlansPersistWorkflowWrites(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	plan := sqlPublishedPlan(t, s, "plan_"+uuid.NewString())
	if err := s.Put(ctx, "execution_plans", plan.ID, plan, 0); err != nil {
		t.Fatal(err)
	}
	var ideaID any
	var path, timestamp, status string
	if err := s.DB.QueryRow("SELECT idea_id,plan_path,timestamp,status FROM plans WHERE plan_id=?", plan.ID).Scan(&ideaID, &path, &timestamp, &status); err != nil {
		t.Fatal(err)
	}
	if ideaID != "i" || path != "/plans/billing.md" || status != "draft" {
		t.Fatalf("plan: %q %q", path, status)
	}
	if _, err := time.Parse(time.RFC3339Nano, timestamp); err != nil {
		t.Fatal(err)
	}
	var old int
	if err := s.DB.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='execution_plans'").Scan(&old); err != nil || old != 0 {
		t.Fatalf("old table remains: %d %v", old, err)
	}
}

func TestIdeaCandidateUsesPrefixedUUID(t *testing.T) {
	s := openTest(t)
	parityIdea(t, s)
	if _, err := s.DB.Exec("INSERT INTO ideation_directions(run_id,direction,status) VALUES('source','expand','queued')"); err != nil {
		t.Fatal(err)
	}
	id, _, err := s.ReserveIdeaCandidate(context.Background(), "source", "expand")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(id, "idea_") || uuid.Validate(strings.TrimPrefix(id, "idea_")) != nil {
		t.Fatalf("candidate must publish a prefixed UUID: %q", id)
	}
}

func TestRequestedSQLStatusConstraints(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.Put(ctx, "projects", "p", model.Project{ID: "p", Path: "/work", Name: "Work"}, 0); err != nil {
		t.Fatal(err)
	}
	plan := sqlPublishedPlan(t, s, "plan")
	if err := s.Put(ctx, "execution_plans", "plan", plan, 0); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		table, key, column string
		valid              []string
	}{
		{"projects", "project_id='p'", "status", []string{"draft", "approved", "error", "complete"}},
		{"ideas", "idea_id='i'", "status", []string{"draft", "approved", "in_process", "complete", "error"}},
		{"plans", "plan_id='plan'", "status", []string{"draft", "approved", "complete", "error"}},
	} {
		for _, state := range tc.valid {
			if _, err := s.DB.Exec("UPDATE "+tc.table+" SET "+tc.column+"=? WHERE "+tc.key, state); err != nil {
				t.Fatalf("%s %s: %v", tc.table, state, err)
			}
		}
		if _, err := s.DB.Exec("UPDATE " + tc.table + " SET " + tc.column + "='invalid' WHERE " + tc.key); err == nil {
			t.Fatalf("%s accepted invalid status", tc.table)
		}
	}
}

func TestPlanSQLLifecycleTracksExecution(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct{ stage, failure, approvedHash, want string }{
		{"plan_approval", "", "", "draft"},
		{"plan_approval", "", "hash", "approved"},
		{"planning", "", "", "draft"},
		{"coding", "", "hash", "approved"},
		{"reviewing", "", "hash", "approved"},
		{"complete", "", "hash", "complete"},
		{"questions", "", "hash", "approved"},
		{"paused", "", "hash", "approved"},
		{"paused", model.GlobalRunnerStopReason, "hash", "approved"},
		{"paused", "provider crashed", "", "error"},
	} {
		t.Run(tc.stage+"/"+tc.want, func(t *testing.T) {
			id := "plan_" + uuid.NewString()
			s := openTest(t)
			p := sqlPublishedPlan(t, s, id)
			p.Stage, p.Error, p.PlanDocument.ApprovedHash = tc.stage, tc.failure, tc.approvedHash
			if err := s.Put(ctx, "execution_plans", id, p, 0); err != nil {
				t.Fatal(err)
			}
			var got string
			if err := s.DB.QueryRow("SELECT status FROM plans WHERE plan_id=?", id).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("status=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestProjectSQLHealthTracksConfigurationAndRemoval(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	p := model.Project{ID: "p", Name: "Work", Path: "/work"}
	if err := s.Put(ctx, "projects", p.ID, p, 0); err != nil {
		t.Fatal(err)
	}
	for n, tc := range []struct{ reported, want string }{{"ERR", "error"}, {"OK", "draft"}} {
		health := []model.ProjectStatus{{Path: "/work", Status: tc.reported}}
		if err := s.Put(ctx, "project_status", "configured", health, n); err != nil {
			t.Fatal(err)
		}
		var got string
		if err := s.DB.QueryRow("SELECT status FROM projects WHERE project_id='p'").Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Fatalf("health=%q want %q", got, tc.want)
		}
	}
	p.Name = "Renamed"
	if err := s.Put(ctx, "projects", p.ID, p, 1); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := s.DB.QueryRow("SELECT project_name FROM projects WHERE project_id='p'").Scan(&name); err != nil || name != "Renamed" {
		t.Fatalf("rename: %q %v", name, err)
	}
	if err := s.UnregisterProject(ctx, "p", 2); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.DB.QueryRow("SELECT count(*) FROM projects WHERE project_id='p'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("removed registration remains: %d %v", count, err)
	}
}

func TestPlanSQLHumanWaitPreservesDraftDespiteExplanationInError(t *testing.T) {
	s := openTest(t)
	p := sqlPublishedPlan(t, s, "plan")
	p.Stage, p.Error, p.Wait = "paused", "Human decision pending", &model.ExecutionPlanWait{Kind: "human_decision"}
	if err := s.Put(context.Background(), "execution_plans", p.ID, p, 0); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := s.DB.QueryRow("SELECT status FROM plans WHERE plan_id='plan'").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "draft" {
		t.Fatalf("human wait mislabeled: %q", status)
	}
}

func TestObsoleteSQLStructureIsDiscardedOnOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE resources(kind TEXT,id TEXT,version INTEGER,data TEXT,updated_at TEXT,PRIMARY KEY(kind,id));
	 INSERT INTO resources VALUES('projects','old',1,'{}','now');
	 CREATE TABLE execution_plans(plan_id TEXT PRIMARY KEY);
	 INSERT INTO execution_plans VALUES('old');
	 CREATE TABLE plan_versions(plan_id TEXT REFERENCES execution_plans(plan_id));
	 INSERT INTO plan_versions VALUES('old');
	 CREATE VIRTUAL TABLE proposals_fts USING fts5(terms);
	 INSERT INTO proposals_fts VALUES('obsolete proposal');
	 CREATE TABLE ideas(idea_id TEXT PRIMARY KEY,what TEXT);`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("obsolete database must be replaced: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for _, table := range []string{"resources", "projects", "ideas", "plans"} {
		var count int
		if err := s.DB.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s retained obsolete data: %d %v", table, count, err)
		}
	}
	var count int
	if err := s.DB.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='execution_plans'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("obsolete table retained: %d %v", count, err)
	}
	if err := s.DB.QueryRow("PRAGMA foreign_keys").Scan(&count); err != nil || count != 1 {
		t.Fatalf("foreign key enforcement not restored: %d %v", count, err)
	}
}

// This fixture represents a previously verified publication. Native workflow
// tests cover the actual ACP attempt, file verification and atomic publication.
func sqlPublishedPlan(t *testing.T, s *Store, id string) model.ExecutionPlan {
	t.Helper()
	idea := parityIdea(t, s)
	ctx := context.Background()
	if err := s.Put(ctx, "ideas", "i", idea, 0); err != nil {
		t.Fatal(err)
	}
	// A prior verified publication precedes the linked completion transition.
	if _, err := s.DB.Exec("INSERT INTO plans(plan_id,idea_id,plan_path,status,timestamp) VALUES(?,'i','/plans/prior.md','draft',?)", id, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	for n, status := range []string{"approved", "in_process", "complete"} {
		idea["decision_status"] = status
		if status == "complete" {
			idea["plan_id"] = id
		}
		if err := s.Put(ctx, "ideas", "i", idea, n+1); err != nil {
			t.Fatal(err)
		}
	}
	return model.ExecutionPlan{ID: id, IdeaID: "i", ProjectID: "p", IdeationRunID: "source", Stage: "plan_approval", PlanVersion: 1, PlanDocument: &model.PlanDocument{Path: "/plans/billing.md", Hash: "hash"}}
}
