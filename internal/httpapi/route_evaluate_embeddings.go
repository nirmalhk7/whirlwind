package httpapi

import (
	"encoding/json"
	ctxengine "whirlwind/internal/context"
	"whirlwind/internal/model"
)

func (q apiRequest) evaluateEmbeddings() error {
	p, e := q.Engine.Project(q.r.Context(), q.id)
	if e != nil {
		return e
	}
	if p.Policy.Embeddings == nil {
		return &apiError{422, "invalid_input", "Configure embedding provider in committed policy"}
	}
	var input struct {
		Fixtures []ctxengine.Fixture `json:"fixtures"`
	}
	if e = json.Unmarshal(q.body, &input); e != nil {
		return e
	}
	result, e := q.evaluateIndex(*p.Policy.Embeddings, input.Fixtures)
	if e != nil {
		return e
	}
	reply(q.w, 200, result)
	return nil
}

func (q apiRequest) evaluateIndex(embeddings model.Embeddings, fixtures []ctxengine.Fixture) (ctxengine.Evaluation, error) {
	idx, record, e := readRecord[ctxengine.Index](q, "indexes")
	if e != nil {
		return ctxengine.Evaluation{}, e
	}
	result, e := idx.Evaluate(q.r.Context(), embeddings, fixtures)
	if e != nil {
		return ctxengine.Evaluation{}, e
	}
	if e = q.Store.Put(q.r.Context(), "indexes", q.id, idx, record.Version); e != nil {
		return ctxengine.Evaluation{}, e
	}
	return result, nil
}
