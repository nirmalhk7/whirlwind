package store

import (
	"context"
	"encoding/json"
	"strings"
	"unicode"
	"whirlwind/internal/model"
)

func fullTextQuery(query string) string {
	words := strings.FieldsFunc(query, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	seen := map[string]bool{}
	terms := []string{}
	for _, w := range words {
		w = strings.ToLower(w)
		if len(w) < 3 || seen[w] {
			continue
		}
		seen[w] = true
		terms = append(terms, `"`+w+`"`)
		if len(terms) == 64 {
			break
		}
	}
	if len(terms) == 0 {
		return `""`
	}
	return strings.Join(terms, " OR ")
}

// MatchingLessons searches advice text only; full-query structured matches outrank BM25.
func (s *Store) MatchingLessons(ctx context.Context, project, role, query string) ([]model.Record, error) {
	statement := `WITH prose AS MATERIALIZED (
 SELECT lesson_search.rowid,bm25(lesson_search,0,0,0,3,2,4,8,8,10) AS score
 FROM lesson_search JOIN resources active ON active.rowid=lesson_search.rowid WHERE lesson_search MATCH ? AND project=? AND role=? AND json_extract(active.data,'$.status') IN ('tentative','corroborated') ORDER BY score LIMIT 128
 ), exact AS MATERIALIZED (
 SELECT lesson,SUM(weight) AS weight FROM lesson_identifiers WHERE project=? AND role=? AND instr(lower(?),value)>0 GROUP BY lesson
 ), candidates AS (SELECT rowid FROM prose UNION SELECT lesson FROM exact)
 SELECT r.rowid,r.id,r.version,r.data,r.updated_at,COALESCE(exact.weight,0)
 FROM candidates c JOIN resources r ON r.rowid=c.rowid LEFT JOIN prose p ON p.rowid=c.rowid LEFT JOIN exact ON exact.lesson=c.rowid
 WHERE json_extract(r.data,'$.status') IN ('tentative','corroborated')
 ORDER BY COALESCE(exact.weight,0) DESC,COALESCE(p.score,0),CASE json_extract(r.data,'$.status') WHEN 'corroborated' THEN 0 ELSE 1 END,r.updated_at DESC,r.id LIMIT 128`
	rows, err := s.DB.QueryContext(ctx, statement, fullTextQuery(query), project, role, project, role, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []model.Record{}
	for rows.Next() {
		var r model.Record
		var b []byte
		var exact int
		if err = rows.Scan(&r.CreatedOrder, &r.ID, &r.Version, &b, &r.UpdatedAt, &exact); err != nil {
			return nil, err
		}
		r.MatchReason = "BM25 prose match"
		if exact > 0 {
			r.MatchReason = "exact source identifier"
		}
		r.Data = json.RawMessage(b)
		out = append(out, r)
	}
	return out, rows.Err()
}
