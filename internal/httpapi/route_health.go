package httpapi

import ()

func (q apiRequest) health() error {
	reply(q.w, 200, map[string]string{"status": "ok"})
	return nil
}
