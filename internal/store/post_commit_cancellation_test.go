package store

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"strings"
	"testing"

	sqlite "modernc.org/sqlite"

	"whirlwind/internal/model"
)

// Wrap only the commit boundary. Statements, constraints, transactions and log
// files still use real SQLite; cancellation fires after its successful commit.
type cancelAfterCommitDriver struct {
	driver.Driver
	cancel context.CancelFunc
}

func (d cancelAfterCommitDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.Driver.Open(name)
	if err != nil {
		return nil, err
	}
	return cancelAfterCommitConn{Conn: conn, cancel: d.cancel}, nil
}

type cancelAfterCommitConn struct {
	driver.Conn
	cancel context.CancelFunc
}

func (c cancelAfterCommitConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	begin, ok := c.Conn.(driver.ConnBeginTx)
	if !ok {
		return nil, errors.New("SQLite connection lacks BeginTx")
	}
	tx, err := begin.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return cancelAfterCommitTx{Tx: tx, cancel: c.cancel}, nil
}

type cancelAfterCommitTx struct {
	driver.Tx
	cancel context.CancelFunc
}

func (tx cancelAfterCommitTx) Commit() error {
	if err := tx.Tx.Commit(); err != nil {
		return err
	}
	tx.cancel()
	return nil
}

func TestCommittedPlanTransitionRetainsLogsAfterCallerCancellation(t *testing.T) {
	for _, kind := range []string{"put", "write", "batch"} {
		t.Run(kind, func(t *testing.T) { assertCommittedPlanLogs(t, kind) })
	}
}
func assertCommittedPlanLogs(t *testing.T, kind string) {
	t.Helper()
	s, path, logPath, plan := planLogTransitionFixture(t)
	ctx := context.Background()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	defer cancel()
	name := "cancel-after-commit-" + ID()
	sql.Register(name, cancelAfterCommitDriver{Driver: &sqlite.Driver{}, cancel: cancel})
	dsn, err := sqliteURI(path, false)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open(name, dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	s = &Store{DB: db, runLogs: newRunLogStorage(path)}
	t.Cleanup(func() { _ = s.Close() })
	plan.Stage = "review"
	plan.Handoff.NextAction = "committed-transition-log-marker"
	err = writePlanTransition(canceled, s, kind, plan)
	if canceled.Err() != context.Canceled {
		t.Fatal("fixture did not cancel caller after commit")
	}
	row, readErr := s.Get(ctx, "execution_plans", plan.ID)
	if readErr != nil || row.Version != 2 {
		t.Fatalf("real SQLite transition was not committed: v%d %v", row.Version, readErr)
	}
	if err != nil {
		t.Fatalf("committed transition reported false failure: %v", err)
	}
	body, err := os.ReadFile(logPath)
	if err != nil || !strings.Contains(string(body), plan.Handoff.NextAction) {
		t.Fatalf("committed transition lost persistent log: %v", err)
	}
}
func writePlanTransition(ctx context.Context, s *Store, kind string, plan model.ExecutionPlan) error {
	change := Change{Kind: "execution_plans", ID: plan.ID, Value: plan, Expected: 1}
	switch kind {
	case "put":
		return s.Put(ctx, change.Kind, change.ID, change.Value, change.Expected)
	case "write":
		return s.Write(ctx, []Change{change}, nil)
	default:
		staged, batch := Stage(ctx)
		if err := s.Put(staged, change.Kind, change.ID, change.Value, change.Expected); err != nil {
			return err
		}
		return batch.Commit(staged, s, nil)
	}
}

func planLogTransitionFixture(t *testing.T) (*Store, string, string, model.ExecutionPlan) {
	t.Helper()
	s, path, logPath := persistedLogFixture(t)
	ctx := context.Background()
	plan := model.ExecutionPlan{ID: "plan", Stage: "coding"}
	if err := s.Put(ctx, "execution_plans", plan.ID, plan, 0); err != nil {
		t.Fatal(err)
	}
	attempt := model.Attempt{AttemptID: persistedRunID, Task: plan.ID, Role: "code", Status: "complete", Finished: true}
	if err := s.Put(ctx, "sessions", persistedRunID, attempt, 1); err != nil {
		t.Fatal(err)
	}
	return s, path, logPath, plan
}

func TestCanceledPlanTransitionDoesNotCommitOrChangeLogs(t *testing.T) {
	for _, kind := range []string{"put", "write", "batch"} {
		t.Run(kind, func(t *testing.T) { assertCanceledPlanTransition(t, kind) })
	}
}
func assertCanceledPlanTransition(t *testing.T, kind string) {
	t.Helper()
	s, _, path, plan := planLogTransitionFixture(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	plan.Stage = "review"
	if err := writePlanTransition(ctx, s, kind, plan); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled write did not fail before commit: %v", err)
	}
	row, err := s.Get(context.Background(), "execution_plans", plan.ID)
	if err != nil || row.Version != 1 {
		t.Fatalf("canceled write changed durable plan: v%d %v", row.Version, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("canceled write changed persistent log: %v", err)
	}
}
