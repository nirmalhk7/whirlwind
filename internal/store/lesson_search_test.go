package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"whirlwind/internal/model"
)

const lessonSearchDatabase = "state.db"

func TestLessonSearchRanksRareTermsAndIgnoresMetadata(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), lessonSearchDatabase))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	lessons := []model.Lesson{
		{ID: "specific", Trigger: "sqlite contention", Correction: "Use bounded transaction retries"},
		{ID: "generic", Trigger: "sqlite", Correction: "sqlite sqlite sqlite sqlite"},
		{ID: "metadata", Trigger: "other", Correction: "other", Provider: "contention"},
	}
	for _, l := range lessons {
		l.ProjectID = "p"
		l.Role = "code"
		l.Status = "tentative"
		if err = s.Put(ctx, "lessons", l.ID, l, 0); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.MatchingLessons(ctx, "p", "code", "sqlite contention")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "specific" {
		t.Fatalf("ranked lesson results: %+v", got)
	}
}

func TestLessonSearchDoesNotMatchSubstringsAndTracksRetirement(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), lessonSearchDatabase))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	l := model.Lesson{ID: "l", ProjectID: "p", Role: "plan", Status: "tentative", Trigger: "transactional"}
	if err = s.Put(ctx, "lessons", l.ID, l, 0); err != nil {
		t.Fatal(err)
	}
	got, err := s.MatchingLessons(ctx, "p", "plan", "transaction")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatal("substring matched unrelated word")
	}
	if _, err = s.RetireLesson(ctx, l.ID, 1); err != nil {
		t.Fatal(err)
	}
	got, err = s.MatchingLessons(ctx, "p", "plan", "transactional")
	if err != nil || len(got) != 0 {
		t.Fatalf("retired recall: %v %v", got, err)
	}
}

func TestInactiveLessonsDoNotConsumeProseCandidateWindow(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), lessonSearchDatabase))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	changes := []Change{{Kind: "lessons", ID: "active", Value: model.Lesson{ID: "active", ProjectID: "p", Role: "code", Status: "tentative", Trigger: "contention transaction retry with extra details"}}}
	for i := range 130 {
		id := fmt.Sprint(i)
		changes = append(changes, Change{Kind: "lessons", ID: id, Value: model.Lesson{ID: id, ProjectID: "p", Role: "code", Status: "retired", Trigger: "contention"}})
	}
	if err := s.Write(ctx, changes, nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.MatchingLessons(ctx, "p", "code", "contention")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "active" {
		t.Fatalf("inactive lessons hid current candidate: %+v", got)
	}
}
