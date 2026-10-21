package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"whirlwind/internal/model"
)

func BenchmarkLessonRetrieval(b *testing.B) {
	for _, size := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			s, err := Open(filepath.Join(b.TempDir(), "lessons.db"))
			if err != nil {
				b.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			seedBenchmarkLessons(b, s, size)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				matches, err := s.MatchingLessons(context.Background(), "benchmark", "code", "transaction contention")
				if err != nil || len(matches) == 0 || matches[0].ID != "specific" {
					b.Fatalf("retrieval lost relevant lesson: %v %v", matches, err)
				}
			}
		})
	}
}

func seedBenchmarkLessons(b *testing.B, s *Store, size int) {
	b.Helper()
	ctx := context.Background()
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		for i := 0; i < size; i++ {
			lesson := model.Lesson{ID: fmt.Sprint(i), ProjectID: "benchmark", Role: "code", Status: "tentative", Trigger: "transaction", Mistake: "generic operation", Correction: "check the operation"}
			if i == size-1 {
				lesson.ID = "specific"
				lesson.Trigger = "transaction contention"
				lesson.Correction = "Use bounded retries"
			}
			if err := put(ctx, tx, "lessons", lesson.ID, lesson, 0); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
}
