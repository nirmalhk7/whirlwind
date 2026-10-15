package httpapi

import ()

func (q apiRequest) overview() error {
	result := map[string]any{}
	for _, k := range []string{"projects", "tasks", "directions", "inbox", "providers"} {
		v, e := q.Store.List(q.r.Context(), k)
		if e != nil {
			return e
		}
		result[k] = v
	}
	reply(q.w, 200, result)
	return nil
}
