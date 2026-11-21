// Package queuehttp runs queue API fixtures without opening a network socket.
package queuehttp

import (
	"net/http"
	"net/http/httptest"
)

type Transport struct{ Handler http.Handler }

func (t Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	response := httptest.NewRecorder()
	t.Handler.ServeHTTP(response, req)
	return response.Result(), nil
}
