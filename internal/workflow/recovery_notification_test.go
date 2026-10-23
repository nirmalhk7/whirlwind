package workflow

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

func TestConcurrentFailuresCreateSingleNotice(t *testing.T) {
	e, task, _ := testEngine(t)
	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() { defer group.Done(); e.fail(context.Background(), task.ID, errors.New("chain exhausted")) }()
	}
	group.Wait()
	records, err := e.Store.List(context.Background(), "inbox")
	records = recoveryNotifications(records)
	if err != nil || len(records) != 1 {
		t.Fatalf("notices=%d error=%v", len(records), err)
	}
}

func TestRecoveryNoticeFailureRollsBackTaskPause(t *testing.T) {
	e, task, _ := testEngine(t)
	_, err := e.Store.DB.Exec("CREATE TRIGGER reject_notice BEFORE INSERT ON resources WHEN NEW.kind='inbox' BEGIN SELECT RAISE(ABORT,'notice failure'); END")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.pauseFailure(context.Background(), task.ID, errors.New("chain exhausted")); err == nil {
		t.Fatal("notification failure swallowed")
	}
	record, err := e.Store.Get(context.Background(), "tasks", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	current, err := store.Decode[model.Task](record)
	if err != nil || current.Stage != task.Stage {
		t.Fatalf("task paused without notice: %+v %v", current, err)
	}
}

func TestDispatchExhaustionPausesTaskAndNotifiesInbox(t *testing.T) {
	h := newRetryHarness(t)
	calls := 0
	h.engine.Adapter = func(model.Provider) provider.Adapter {
		return connectionAdapterFunc(func(context.Context, provider.Input, provider.Hooks) (provider.Output, error) {
			calls++
			return provider.Output{}, io.EOF
		})
	}
	ctx := context.Background()
	h.engine.dispatch(ctx)
	h.engine.background.Wait()
	record, err := h.engine.Store.Get(ctx, "tasks", h.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Decode[model.Task](record)
	if err != nil || task.Stage != "paused" || task.ResumeStage != "planning" {
		t.Fatalf("task did not pause at failed stage: %+v %v", task, err)
	}
	notices, err := h.engine.Store.List(ctx, "inbox")
	notices = recoveryNotifications(notices)
	if err != nil || len(notices) != 1 || calls != 3 {
		t.Fatalf("attempts=%d notices=%d error=%v", calls, len(notices), err)
	}
}

func recoveryNotifications(records []model.Record) []model.Record {
	var result []model.Record
	for _, record := range records {
		notice, _ := store.Decode[model.Notification](record)
		if notice.Kind == "recovery" || notice.Kind == "task_paused" || notice.Kind == "task_waiting_quota" {
			result = append(result, record)
		}
	}
	return result
}
