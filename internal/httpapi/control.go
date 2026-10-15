package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"whirlwind/internal/store"
)

// control validates local access before routing reads or serialized mutations.
func (s *Server) control(w http.ResponseWriter, r *http.Request) {
	private(w)
	if err := validateControl(r); err != nil {
		s.error(w, err)
		return
	}
	if r.Method == "GET" {
		s.readControl(w, r)
		return
	}
	if err := s.mutate(w, r); err != nil {
		s.error(w, err)
	}
}
func validateControl(r *http.Request) error {
	if !local(r.Host) {
		return &apiError{403, "local_host_required", "Loopback Host required"}
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
		return &apiError{403, "origin_rejected", "Origin must match local host"}
	}
	return nil
}
func (s *Server) readControl(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/v1/events" {
		s.events(w, r)
	} else {
		s.route(w, r, nil)
	}
}

type mutation struct {
	key, fingerprint string
	body             []byte
}

func readMutation(w http.ResponseWriter, r *http.Request) (mutation, error) {
	m := mutation{key: r.Header.Get("Idempotency-Key")}
	if m.key == "" || len(m.key) > 256 {
		return m, &apiError{428, "precondition_required", "Idempotency-Key required"}
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024*1024))
	if err != nil {
		return m, &apiError{413, "request_too_large", "Request exceeds 1 MiB"}
	}
	if len(body) == 0 {
		body = []byte("{}")
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(body, &object) != nil || object == nil {
		return m, &apiError{422, "invalid_input", "JSON object required"}
	}
	sum := sha256.Sum256([]byte(r.Method + " " + r.URL.RequestURI() + " " + r.Header.Get("If-Match") + " " + string(body)))
	m.body = body
	m.fingerprint = hex.EncodeToString(sum[:])
	return m, nil
}
func (s *Server) mutate(w http.ResponseWriter, r *http.Request) error {
	if r.Method != "POST" && r.Method != "PATCH" && r.Method != "DELETE" {
		return &apiError{404, "route_not_found", "Unsupported route"}
	}
	m, err := readMutation(w, r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	status, cached, err := s.Store.Idempotent(r.Context(), m.key, m.fingerprint)
	if err != nil {
		return err
	}
	if status != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(cached)
		return nil
	}
	return s.executeMutation(w, r, m)
}
func (s *Server) executeMutation(w http.ResponseWriter, r *http.Request, m mutation) error {
	journal, err := s.prepareMutation(w, r, m)
	if err != nil {
		return err
	}
	ctx, batch := store.Stage(r.Context())
	r = r.WithContext(ctx)
	buffer := &responseBuffer{header: make(http.Header)}
	s.route(buffer, r, m.body)
	if buffer.status == 0 {
		buffer.status = 200
	}
	if err := s.commitMutation(r, batch, buffer, m, journal); err != nil {
		return err
	}
	maps.Copy(w.Header(), buffer.header)
	w.WriteHeader(buffer.status)
	_, _ = w.Write(buffer.body)
	return nil
}
