package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"whirlwind/internal/model"
)

// ProjectRoleMemory is bounded independently of the generic resource history
// and its event stream. Replacing its lessons never copies notes to events.
type ProjectRoleMemory struct {
	Version       int
	PolicyHash    string
	PolicyVersion int
	Lessons       []model.RoleLesson
}

func (s *Store) ProjectRoleMemory(ctx context.Context, project, role string) (ProjectRoleMemory, error) {
	var memory ProjectRoleMemory
	var raw []byte
	err := s.DB.QueryRowContext(ctx, "SELECT version,policy_hash,policy_version,lessons FROM project_role_memory WHERE project_id=? AND role=?", project, role).
		Scan(&memory.Version, &memory.PolicyHash, &memory.PolicyVersion, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return memory, nil
	}
	if err != nil {
		return memory, err
	}
	return memory, json.Unmarshal(raw, &memory.Lessons)
}

func (s *Store) SaveProjectRoleMemory(ctx context.Context, project, role string, memory ProjectRoleMemory, preconditions ...Change) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		if err := assertMemoryPreconditions(ctx, tx, preconditions); err != nil {
			return err
		}
		return writeProjectRoleMemory(ctx, tx, project, role, memory)
	})
}

func assertMemoryPreconditions(ctx context.Context, tx *sql.Tx, preconditions []Change) error {
	for _, check := range preconditions {
		if !check.isVersionPrecondition() {
			return ErrConflict
		}
		if err := assertResourceVersionTx(ctx, tx, check.Kind, check.ID, check.Expected); err != nil {
			return err
		}
	}
	return nil
}

func writeProjectRoleMemory(ctx context.Context, tx *sql.Tx, project, role string, memory ProjectRoleMemory) error {
	raw, err := json.Marshal(memory.Lessons)
	if err != nil {
		return err
	}
	lessons := string(raw)
	var result sql.Result
	if memory.Version == 0 {
		result, err = tx.ExecContext(ctx, "INSERT INTO project_role_memory VALUES(?,?,1,?,?,?) ON CONFLICT(project_id,role) DO NOTHING", project, role, memory.PolicyHash, memory.PolicyVersion, lessons)
	} else {
		result, err = tx.ExecContext(ctx, "UPDATE project_role_memory SET version=version+1,policy_hash=?,policy_version=?,lessons=? WHERE project_id=? AND role=? AND version=?",
			memory.PolicyHash, memory.PolicyVersion, lessons, project, role, memory.Version)
	}
	if err != nil {
		return err
	}
	return requireOneRow(result)
}
