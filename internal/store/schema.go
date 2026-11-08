package store

import _ "embed"

//go:embed schema.sql
var currentSchema string

// initializeSchema creates the current database layout atomically. Schema upgrades are unsupported.
func (s *Store) initializeSchema() error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(currentSchema); err != nil {
		return err
	}
	return tx.Commit()
}
