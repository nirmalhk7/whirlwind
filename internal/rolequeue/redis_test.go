package rolequeue

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRedisRoleQueuesFIFOReplayAndRecovery(t *testing.T) {
	address := os.Getenv("WHIRLWIND_TEST_REDIS_URL")
	if address == "" {
		t.Skip("set WHIRLWIND_TEST_REDIS_URL to an isolated Redis instance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	namespace := "whirlwind-test:" + uuid.NewString()
	q, err := Open(address, namespace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, role := range Roles() {
			_ = q.client.Del(context.Background(), namespace+":"+string(role)).Err()
		}
		_ = q.Close()
	})
	a := Delivery{ID: "old", Version: 1, Stage: "coding", UpdatedAt: "old"}
	b := Delivery{ID: "new", Version: 1, Stage: "coding", UpdatedAt: "new"}
	assertHead := func(q *Redis, role Role, want Delivery, candidates ...Delivery) {
		t.Helper()
		head, ok, err := q.Head(ctx, role, candidates)
		if err != nil || ok != (want.ID != "") || head != want {
			t.Fatalf("%s head=%+v (%v, %v), want %+v", role, head, ok, err, want)
		}
	}
	assertHead(q, Coding, a, a, b)
	// Duplicate delivery and a changed producer enumeration cannot overtake a.
	assertHead(q, Coding, a, b, a, a)
	assertHead(q, Reviewer, b, b)
	restarted, err := Open(address, namespace)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restarted.Close() }()
	assertHead(restarted, Coding, a, a, b)
	count, err := restarted.client.ZCard(ctx, namespace+":"+string(Coding)).Result()
	if err != nil || count != 2 {
		t.Fatalf("retry duplicated pending tasks: count=%d err=%v", count, err)
	}
	// Once SQLite advances, the old revision is removed and B can run.
	next := Delivery{ID: a.ID, Version: 2, Stage: "repair", UpdatedAt: "next"}
	assertHead(restarted, Coding, b, b, next)
	assertHead(restarted, Coding, next, next)
	assertHead(restarted, Coding, Delivery{})
	// Redis loss is recoverable solely from the durable candidate projection.
	if err := restarted.client.Del(ctx, namespace+":"+string(Reviewer)).Err(); err != nil {
		t.Fatal(err)
	}
	assertHead(restarted, Reviewer, b, b)
}
