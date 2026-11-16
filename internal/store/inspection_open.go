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
	dsn, err := sqliteURI(absolute, true)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{DB: db, runLogs: newRunLogStorage(absolute)}, nil
}

// Driver pragmas apply to every connection, including pool replacements.
func sqliteURI(path string, readOnly bool) (string, error) {
	query := url.Values{"_pragma": []string{"foreign_keys(1)", "busy_timeout(5000)"}}
	if readOnly {
		query.Set("mode", "ro")
	}
	uri := url.URL{Scheme: "file", RawQuery: query.Encode()}
	if path == ":memory:" && !readOnly {
		uri.Opaque = ":memory:"
	} else {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		uri.Path = absolute
	}
	return uri.String(), nil
}
