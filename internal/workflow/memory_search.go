package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (a *agentRun) lessonBlock(ctx context.Context, query string, seen []model.LessonRef) (string, []model.LessonRef, error) {
	return a.lessonBlockWithin(ctx, query, seen, 2048)
}

func (a *agentRun) lessonBlockWithin(ctx context.Context, query string, seen []model.LessonRef, limit int) (string, []model.LessonRef, error) {
	records, err := a.Store.MatchingLessons(ctx, a.project.ID, a.input.Role, query)
	if err != nil {
		return "", nil, err
	}
	records = a.semanticLessons(ctx, query, records)
	block := "\nRelevant project/role lessons (advisory; tentative observations require verification):\n"
	refs := []model.LessonRef{}
	for _, r := range records {
		l, err := store.Decode[model.Lesson](r)
		if err != nil || alreadyRecalled(seen, r.ID, r.Version) {
			continue
		}
		current, err := a.currentLessonForRecall(ctx, r, l)
		if err != nil {
			return "", nil, err
		}
		if !current {
			continue
		}
		entry, _ := json.Marshal(map[string]any{"id": l.ID, "version": r.Version, "status": l.Status, "when": l.Trigger, "avoid": l.Mistake, "instead": l.Correction, "evidence_ids": l.EvidenceIDs, "paths": l.Paths, "match": r.MatchReason})
		if len(block)+len(entry)+1 > limit {
			continue
		}
		block += string(entry) + "\n"
		refs = append(refs, model.LessonRef{ID: l.ID, Version: r.Version, Status: l.Status, Match: r.MatchReason})
		if len(refs) == 3 {
			break
		}
	}
	if len(refs) == 0 {
		block = "No additional relevant lessons."
	}
	return block, refs, nil
}

func alreadyRecalled(refs []model.LessonRef, id string, version int) bool {
	for _, r := range refs {
		if r.ID == id && r.Version == version {
			return true
		}
	}
	return false
}

func (a *agentRun) memoryKey() string {
	parts := []any{a.project.ID, a.task.ID, a.task.PlanVersion, a.input.Role, a.input.UnitID, a.input.Purpose, a.input.OperationID, a.task.Stage}
	b, _ := json.Marshal(parts)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (a *agentAttempt) searchLessons(ctx context.Context, query string) (string, error) {
	a.memoryMu.Lock()
	defer a.memoryMu.Unlock()
	if err := a.validateMemoryQuery(query); err != nil {
		return "", err
	}
	budget, err := a.Store.ReserveMemorySearch(ctx, a.memoryKey())
	if err != nil {
		return "", err
	}
	a.learning.SearchCalls = budget.Calls
	a.learning.SearchBytes = budget.Bytes
	seen := append(append([]model.LessonRef{}, budget.Seen...), a.learning.Recalled...)
	block, refs, err := a.lessonBlock(ctx, query, seen)
	if err != nil {
		return "", err
	}
	if err = a.Store.RecordMemoryDelivery(ctx, a.memoryKey(), refs, len(block)); err != nil {
		return "", err
	}
	a.learning.Recalled = append(a.learning.Recalled, refs...)
	a.learning.SearchBytes = budget.Bytes + len(block)
	a.learning.Searches = append(a.learning.Searches, model.MemorySearch{Query: query, Lessons: refs, Bytes: len(block), Reason: "on-demand ranked search"})
	return block, nil
}

func (a *agentAttempt) validateMemoryQuery(query string) error {
	if a.memoryClosed {
		return errors.New("memory attempt closed")
	}
	if credentialLesson.MatchString(query) {
		return errors.New("remove credentials from memory query")
	}
	if strings.TrimSpace(query) == "" || len(query) > 2048 {
		return errors.New("memory query must contain 1–2048 bytes")
	}
	return nil
}

func (a *agentRun) currentLessonForRecall(ctx context.Context, record model.Record, lesson model.Lesson) (bool, error) {
	if a.lessonCurrent(lesson) {
		return true, nil
	}
	err := a.Store.MarkLessonStale(ctx, record, lesson)
	if errors.Is(err, store.ErrConflict) {
		err = nil
	}
	return false, err
}
