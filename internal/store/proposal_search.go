package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"whirlwind/internal/model"
)

// SQLite trim needs an explicit character set to match Go strings.TrimSpace,
// which the former component index used when normalizing stored paths.
const componentWhitespace = "\t\n\v\f\r \u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000"

// SearchProposals combines indexed project/category filters and JSON component
// filters with FTS5 matches and recent records.
func (s *Store) SearchProposals(ctx context.Context, query model.ProposalQuery) ([]model.Proposal, error) {
	if query.ProjectID == "" {
		return nil, errors.New("proposal history requires a project ID")
	}
	limit := query.Limit
	if limit <= 0 || limit > 12 {
		limit = 12
	}
	seen := map[string]bool{}
	proposals := []model.Proposal{}
	terms := ftsQuery(query.Terms)
	if terms != "" {
		matchLimit := limit / 2
		if matchLimit == 0 {
			matchLimit = 1
		}
		matches, err := s.searchProposalRows(ctx, query, terms, matchLimit, seen)
		if err != nil {
			return nil, err
		}
		proposals = append(proposals, matches...)
	}
	if len(proposals) < limit {
		recent, err := s.searchProposalRows(ctx, query, "", limit-len(proposals), seen)
		if err != nil {
			return nil, err
		}
		proposals = append(proposals, recent...)
	}
	return proposals, nil
}

// ProposalComponentCounts returns historical exploration counts for every project component.
func (s *Store) ProposalComponentCounts(ctx context.Context, project string) (map[string]int, error) {
	if strings.TrimSpace(project) == "" {
		return nil, errors.New("proposal component history requires a project ID")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT trim(c.value,?) AS component,count(DISTINCT p.id)
	 FROM proposals p,json_each(p.components) c WHERE p.project_id=?
	 GROUP BY component HAVING component<>''`, componentWhitespace, project)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	counts := map[string]int{}
	for rows.Next() {
		var component string
		var count int
		if err := rows.Scan(&component, &count); err != nil {
			return nil, err
		}
		counts[component] = count
	}
	return counts, rows.Err()
}

func (s *Store) searchProposalRows(ctx context.Context, query model.ProposalQuery, terms string, limit int, seen map[string]bool) ([]model.Proposal, error) {
	conditions := []string{"p.project_id=?"}
	args := []any{query.ProjectID}
	if query.Category != "" {
		conditions = append(conditions, "p.category=?")
		args = append(args, query.Category)
	}
	if len(query.Statuses) > 0 {
		placeholders := make([]string, len(query.Statuses))
		for i, status := range query.Statuses {
			placeholders[i] = "?"
			args = append(args, status)
		}
		conditions = append(conditions, "p.status IN ("+strings.Join(placeholders, ",")+")")
	}
	if len(query.Components) > 0 {
		placeholders := make([]string, len(query.Components))
		args = append(args, componentWhitespace, componentWhitespace)
		for i, component := range query.Components {
			placeholders[i] = "?"
			args = append(args, component)
		}
		conditions = append(conditions, "EXISTS (SELECT 1 FROM json_each(p.components) pc WHERE trim(pc.value,?)<>'' AND trim(pc.value,?) IN ("+strings.Join(placeholders, ",")+"))")
	}
	from := "proposals p"
	order := "p.created_at DESC,p.id"
	if terms != "" {
		from = "proposals_fts JOIN proposals p ON p.project_id=proposals_fts.project_id AND p.id=proposals_fts.proposal_id"
		conditions = append(conditions, "proposals_fts MATCH ?")
		args = append(args, terms)
		order = "bm25(proposals_fts),p.created_at DESC"
	}
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, "SELECT p.id,p.data FROM "+from+" WHERE "+strings.Join(conditions, " AND ")+" ORDER BY "+order+" LIMIT ?", args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var proposals []model.Proposal
	for rows.Next() {
		var id string
		var data []byte
		if err := rows.Scan(&id, &data); err != nil {
			return nil, err
		}
		if seen[id] {
			continue
		}
		var proposal model.Proposal
		if err := json.Unmarshal(data, &proposal); err != nil {
			return nil, err
		}
		seen[id] = true
		proposals = append(proposals, proposal)
	}
	return proposals, rows.Err()
}

func ftsQuery(input string) string {
	words := strings.FieldsFunc(strings.ToLower(input), func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') })
	if len(words) > 16 {
		words = words[:16]
	}
	for i := range words {
		words[i] = `"` + strings.ReplaceAll(words[i], `"`, `""`) + `"*`
	}
	return strings.Join(words, " OR ")
}
