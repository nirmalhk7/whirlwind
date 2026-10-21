package workflow

import (
	"slices"
	"strings"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func fuseLessons(lexical []model.Record, semantic []semanticMatch, query string) []model.Record {
	scores := map[string]float64{}
	records := map[string]model.Record{}
	for i, record := range lexical {
		scores[record.ID] += 1 / float64(61+i)
		records[record.ID] = record
	}
	for i, match := range semantic {
		scores[match.record.ID] += 1 / float64(61+i)
		r := match.record
		r.MatchReason = "semantic similarity and rank fusion"
		records[r.ID] = r
	}
	out := []model.Record{}
	for _, r := range records {
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b model.Record) int {
		exactA, exactB := exactLessonMatch(a, query), exactLessonMatch(b, query)
		if exactA != exactB {
			return exactB - exactA
		}
		if scores[a.ID] > scores[b.ID] {
			return -1
		}
		if scores[a.ID] < scores[b.ID] {
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

func exactLessonMatch(record model.Record, query string) int {
	lesson, err := store.Decode[model.Lesson](record)
	if err != nil {
		return 0
	}
	query = strings.ToLower(query)
	score := 0
	for _, path := range lesson.Paths {
		if path != "" && strings.Contains(query, strings.ToLower(path)) {
			score += 2
		}
	}
	for _, symbol := range lesson.Symbols {
		if symbol != "" && strings.Contains(query, strings.ToLower(symbol)) {
			score++
		}
	}
	if lesson.ErrorSignature != "" && strings.Contains(query, strings.ToLower(lesson.ErrorSignature)) {
		score += 3
	}
	return score
}
