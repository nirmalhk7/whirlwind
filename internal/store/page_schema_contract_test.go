package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"whirlwind/internal/model"
)

const schemaProjectPath = "/work/project"

// The current Page fixes core column sets; operational metadata belongs in
// linked records rather than extra columns in these three tables.
func TestWhirlwindPlanExactCoreColumns(t *testing.T) {
	s := openTest(t)
	for _, tc := range []struct {
		table string
		want  []string
	}{
		{"ideas", []string{"idea_id", "project_id", "run_id", "idea_title", "idea_type", "what", "why", "where", "how", "status", "timestamp"}},
		{"projects", []string{"project_id", "project_name", "project_path", "status", "timestamp"}},
		{"plans", []string{"plan_id", "idea_id", "plan_path", "status", "timestamp"}},
	} {
		t.Run(tc.table, func(t *testing.T) {
			columns := coreColumnNames(t, s, tc.table)
			if !reflect.DeepEqual(columns, tc.want) {
				t.Fatalf("core columns=%v; approved Page requires %v", columns, tc.want)
			}
		})
	}
}

func TestWhirlwindPlanRequiresExplicitIdeaTitle(t *testing.T) {
	s := openTest(t)
	idea := parityIdea(t, s)
	for _, value := range []string{"", "   "} {
		idea["idea_title"] = value
		if err := s.Put(context.Background(), "ideas", "i", idea, 0); !errors.Is(err, ErrConflict) {
			t.Fatalf("blank idea title accepted: %v", err)
		}
	}
}

func TestWhirlwindPlanRetainsTitleIndependentFromWhat(t *testing.T) {
	s := openTest(t)
	idea := parityIdea(t, s)
	if err := s.Put(context.Background(), "ideas", "i", idea, 0); err != nil {
		t.Fatal(err)
	}
	var title, what, historyTitle string
	if err := s.DB.QueryRow("SELECT idea_title,what FROM ideas WHERE idea_id='i'").Scan(&title, &what); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow("SELECT title FROM proposals WHERE id='i' AND project_id='p'").Scan(&historyTitle); err != nil {
		t.Fatal(err)
	}
	if title != "Invoice exports" || what != "Export invoices" || historyTitle != "Invoice exports" {
		t.Fatalf("title overwritten by content: title=%q what=%q history=%q", title, what, historyTitle)
	}
}

func TestWhirlwindPlanUnregisterPreservesHistoricalProjectReference(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	idea := parityIdea(t, s)
	if err := s.Put(ctx, "ideas", "i", idea, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.UnregisterProject(ctx, "p", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "projects", "p"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("registration retained: %v", err)
	}
	var parent string
	if err := s.DB.QueryRow("SELECT p.project_id FROM ideas i JOIN projects p USING(project_id) WHERE i.idea_id='i'").Scan(&parent); err != nil || parent != "p" {
		t.Fatalf("history lost project identity: %q %v", parent, err)
	}
	if _, err := s.DB.Exec("DELETE FROM projects WHERE project_id='p'"); err == nil {
		t.Fatal("historical idea lost foreign-key protection")
	}
}

func TestWhirlwindPlanCreationTimestampsSurviveUpdatesAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	plan := sqlPublishedPlan(t, s, "plan")
	if err := s.Put(ctx, "execution_plans", "plan", plan, 0); err != nil {
		t.Fatal(err)
	}
	before := coreCreationTimestamps(t, s)
	project, err := s.Get(ctx, "projects", "p")
	if err != nil {
		t.Fatal(err)
	}
	value, _ := Decode[model.Project](project)
	value.Name = "Renamed"
	if err := s.Put(ctx, "projects", "p", value, project.Version); err != nil {
		t.Fatal(err)
	}
	plan.Stage = "coding"
	plan.PlanDocument.ApprovedHash = plan.PlanDocument.Hash
	if err := s.Put(ctx, "execution_plans", "plan", plan, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if after := coreCreationTimestamps(t, s); !reflect.DeepEqual(after, before) {
		t.Fatalf("creation timestamps changed across update/reopen: %v -> %v", before, after)
	}
	if _, err := s.Get(ctx, "execution_plans", "plan"); err != nil {
		t.Fatalf("reopen discarded current state: %v", err)
	}
}

func TestWhirlwindPlanDatabaseRejectsMissingParentsAndUnknownTypes(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	idea := parityIdea(t, s)
	if err := s.Put(ctx, "ideas", "i", idea, 0); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"UPDATE ideas SET project_id='missing' WHERE idea_id='i'",
		"INSERT INTO plans(plan_id,idea_id,plan_path,status,timestamp) VALUES('orphan','missing','/plans/plan.md','draft','now')",
	} {
		if _, err := s.DB.Exec(query); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
			t.Fatalf("missing parent not rejected by foreign key: %v", err)
		}
	}
	for _, typ := range []string{"expand", "optimize", "harden", "simplify"} {
		if _, err := s.DB.Exec("UPDATE ideas SET idea_type=? WHERE idea_id='i'", typ); err != nil {
			t.Fatalf("valid type %s: %v", typ, err)
		}
	}
	if _, err := s.DB.Exec("UPDATE ideas SET idea_type='invented' WHERE idea_id='i'"); err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
		t.Fatalf("unknown idea type accepted: %v", err)
	}
}

func TestWhirlwindPlanDatabaseRejectsNullStatusOnInsertAndUpdate(t *testing.T) {
	s := openTest(t)
	plan := sqlPublishedPlan(t, s, "plan")
	if err := s.Put(context.Background(), "execution_plans", plan.ID, plan, 0); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ table, key, insert string }{
		{"projects", "project_id='p'", `INSERT INTO projects(project_id,project_name,project_path,status,timestamp)
 VALUES('null-project','Project','/work/project',NULL,'2026-11-12T00:00:00Z')`},
		{"ideas", "idea_id='i'", `INSERT INTO ideas(idea_id,project_id,run_id,idea_title,idea_type,what,why,"where",how,status,timestamp)
 VALUES('null-idea','p','source','Export','expand','Export invoices','Accounting needs invoices','billing','Export one month together',NULL,'2026-11-12T00:00:00Z')`},
		{"plans", "plan_id='plan'", `INSERT INTO plans(plan_id,idea_id,plan_path,status,timestamp)
 VALUES('null-plan','i','/plans/billing.md',NULL,'2026-11-12T00:00:00Z')`},
	} {
		for operation, query := range map[string]string{
			"insert": tc.insert,
			"update": "UPDATE " + tc.table + " SET status=NULL WHERE " + tc.key,
		} {
			t.Run(tc.table+"/"+operation, func(t *testing.T) {
				_, err := s.DB.Exec(query)
				if err == nil || !strings.Contains(err.Error(), "NOT NULL constraint failed: "+tc.table+".status") {
					t.Fatalf("NULL status was not rejected by its own constraint: %v", err)
				}
			})
		}
	}
}

// Legacy lifecycle spellings must not slip through an insert-only or
// update-only guard. Exercise SQL directly, independently of service mapping.
func TestWhirlwindPlanDatabaseRejectsLegacyStatuses(t *testing.T) {
	s := openTest(t)
	_ = sqlPublishedPlan(t, s, "plan")
	if _, err := s.DB.Exec(`INSERT INTO ideas SELECT 'status-plan-parent',project_id,run_id,idea_title,idea_type,what,why,"where",how,status,timestamp FROM ideas WHERE idea_id='i'`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ table, key, insert string }{
		{"projects", "project_id='p'", `INSERT INTO projects VALUES('status-project','Project','/work/project',?,'2026-11-12T00:00:00Z')`},
		{"ideas", "idea_id='i'", `INSERT INTO ideas VALUES('status-idea','p','source','Export','expand','Export invoices','Accounting needs invoices','billing','Export one month together',?,'2026-11-12T00:00:00Z')`},
		{"plans", "plan_id='plan'", `INSERT INTO plans VALUES('status-plan','status-plan-parent','/plans/billing.md',?,'2026-11-12T00:00:00Z')`},
	} {
		for _, state := range []string{"", "invalid", "completed", "blocked", "ok", "in_process"} {
			if tc.table == "ideas" && state == "in_process" {
				continue
			}
			assertSQLStatusRejected(t, s, tc.insert, state)
			assertSQLStatusRejected(t, s, "UPDATE "+tc.table+" SET status=? WHERE "+tc.key, state)
		}
	}
}

func assertSQLStatusRejected(t *testing.T, s *Store, query, state string) {
	t.Helper()
	if _, err := s.DB.Exec(query, state); err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
		t.Fatalf("status %q was not rejected by its CHECK: %v; %s", state, err, query)
	}
}

func TestWhirlwindPlanIdeaRejectsMissingLinkedPlan(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	idea := parityIdea(t, s)
	if err := s.Put(ctx, "ideas", "i", idea, 0); err != nil {
		t.Fatal(err)
	}
	idea["plan_id"] = "missing"
	if err := s.Put(ctx, "ideas", "i", idea, 1); err == nil {
		t.Fatal("supporting plan link accepted missing plan")
	}
	row, err := s.Get(ctx, "ideas", "i")
	if err != nil || row.Version != 1 {
		t.Fatalf("invalid link changed saved version: %+v %v", row, err)
	}
}

func TestWhirlwindPlanTitleChangeCannotBypassExactDuplicate(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	idea := parityIdea(t, s)
	if err := s.Put(ctx, "ideas", "i", idea, 0); err != nil {
		t.Fatal(err)
	}
	idea["idea_id"], idea["idea_title"] = "duplicate", "A renamed export"
	if err := s.Put(ctx, "ideas", "duplicate", idea, 0); err == nil {
		t.Fatal("different display title bypassed exact-duplicate protection")
	}
	if _, err := s.Get(ctx, "ideas", "duplicate"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("duplicate leaked into saved ideas: %v", err)
	}
}

func TestWhirlwindPlanForeignKeysSurviveConnectionReplacement(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	idea := parityIdea(t, s)
	if err := s.Put(ctx, "ideas", "i", idea, 0); err != nil {
		t.Fatal(err)
	}
	s.DB.SetMaxIdleConns(0)
	var enabled int
	if err := s.DB.QueryRow("PRAGMA foreign_keys").Scan(&enabled); err != nil || enabled != 1 {
		t.Fatalf("replacement connection disabled foreign keys: %d %v", enabled, err)
	}
	if _, err := s.DB.Exec("UPDATE ideas SET project_id='missing' WHERE idea_id='i'"); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		t.Fatalf("replacement connection admitted orphan: %v", err)
	}
}

func TestWhirlwindPlanReopenReplacesLayoutWithExtraCoreColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(context.Background(), "kept_only_for_test", "old", map[string]bool{"obsolete": true}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("ALTER TABLE ideas ADD COLUMN obsolete_metadata TEXT"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	var columns, count int
	if err := s.DB.QueryRow("SELECT count(*) FROM pragma_table_info('ideas')").Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow("SELECT count(*) FROM resources").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if columns != 11 || count != 0 {
		t.Fatalf("obsolete layout retained: columns=%d old resources=%d", columns, count)
	}
}

func coreColumnNames(t *testing.T, s *Store, table string) []string {
	t.Helper()
	rows, err := s.DB.Query("SELECT name FROM pragma_table_info(?) ORDER BY cid", table)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var columns []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return columns
}

func coreCreationTimestamps(t *testing.T, s *Store) []string {
	t.Helper()
	var values []string
	for _, table := range []string{"projects", "ideas", "plans"} {
		var value string
		if err := s.DB.QueryRow("SELECT timestamp FROM " + table).Scan(&value); err != nil {
			t.Fatal(err)
		}
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil || parsed.Location() != time.UTC {
			t.Fatalf("non-UTC creation timestamp: %q %v", value, err)
		}
		values = append(values, value)
	}
	return values
}

func TestWhirlwindPlanHealthFailureIsVisibleAfterExplicitProjectCompletion(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.Put(ctx, "projects", "p", model.Project{ID: "p", Name: "Project", Path: schemaProjectPath, Status: "complete"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "project_status", "configured", []model.ProjectStatus{{ProjectID: "p", Path: schemaProjectPath, Status: "ERR"}}, 0); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := s.DB.QueryRow("SELECT status FROM projects WHERE project_id='p'").Scan(&status); err != nil || status != "error" {
		t.Fatalf("health failure hidden by completed status: %q %v", status, err)
	}
}

func TestWhirlwindPlanRetainsExplicitProjectSetupFailure(t *testing.T) {
	s := openTest(t)
	if err := s.Put(context.Background(), "projects", "p", model.Project{ID: "p", Name: "Project", Path: schemaProjectPath, Status: "error"}, 0); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := s.DB.QueryRow("SELECT status FROM projects WHERE project_id='p'").Scan(&status); err != nil || status != "error" {
		t.Fatalf("setup failure lost during projection: %q %v", status, err)
	}
}
