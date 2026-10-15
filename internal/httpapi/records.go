package httpapi

import (
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (q apiRequest) expected() (int, error) {
	version, err := expected(q.r)
	if err != nil {
		return 0, &apiError{428, "precondition_required", err.Error()}
	}
	return version, nil
}

// currentRecord preserves If-Match validation before loading a mutation target.
func (q apiRequest) currentRecord(kind string) (model.Record, error) {
	version, err := q.expected()
	if err != nil {
		return model.Record{}, err
	}
	record, err := q.Store.Get(q.r.Context(), kind, q.id)
	if err == nil && record.Version != version {
		err = store.ErrConflict
	}
	return record, err
}

func readRecord[T any](q apiRequest, kind string) (T, model.Record, error) {
	record, err := q.Store.Get(q.r.Context(), kind, q.id)
	var value T
	if err == nil {
		value, err = store.Decode[T](record)
	}
	return value, record, err
}

func readCurrent[T any](q apiRequest, kind string) (T, model.Record, error) {
	record, err := q.currentRecord(kind)
	var value T
	if err == nil {
		value, err = store.Decode[T](record)
	}
	return value, record, err
}
