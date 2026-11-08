package store

import (
	"database/sql"
	"net/url"
	"path/filepath"
)

// OpenReadOnly inspects existing supervisor state without schema initialization, recovery,
// settings refreshes, or creation of a replacement database.
func OpenReadOnly(path string) (*Store, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	uri := url.URL{Scheme: "file", Path: absolute, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{DB: db}, nil
}
