package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"whirlwind/internal/model"
	"whirlwind/internal/taskqueue"
)

const roleLease = 2 * time.Minute

// stageRoleTaskTx records eligibility in the exact SQLite transaction that
// committed the new plan revision. The queue API publishes committed rows.
func stageRoleTaskTx(ctx context.Context, tx *sql.Tx, id string, data []byte) error {
	var p model.ExecutionPlan
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	var current int
	if err := tx.QueryRowContext(ctx, "SELECT version FROM resources WHERE kind='execution_plans' AND id=?", id).Scan(&current); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM role_tasks WHERE plan_id=? AND version<>?", id, current); err != nil {
		return err
	}
	return insertRoleTaskTx(ctx, tx, id, current, p, "")
}

func insertRoleTaskTx(ctx context.Context, tx *sql.Tx, id string, version int, p model.ExecutionPlan, required taskqueue.Role) error {
	var policyHash string
	err := tx.QueryRowContext(ctx, "SELECT COALESCE(json_extract(data,'$.policy_hash'),'') FROM resources WHERE kind='projects' AND id=?", p.ProjectID).Scan(&policyHash)
	if errors.Is(err, sql.ErrNoRows) && required == "" {
		return nil
	}
	if err != nil {
		return err
	}
	role := taskqueue.ForPlan(p, policyHash)
	if required != "" && required != role {
		return taskqueue.ErrConflict
	}
	if role == "" {
		return nil
	}
	_, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO role_tasks(role,plan_id,project_id,version,stage) VALUES(?,?,?,?,?)", role, id, p.ProjectID, version, p.Stage)
	return err
}

// BackfillRoleTasks enrolls active revisions from an existing current schema,
// without resetting its state or changing existing queue order.
func (s *Store) BackfillRoleTasks(ctx context.Context) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT p.id,p.version,p.data FROM resources p
		WHERE p.kind='execution_plans' AND NOT EXISTS (SELECT 1 FROM role_tasks q WHERE q.plan_id=p.id AND q.version=p.version)
		ORDER BY julianday(p.updated_at),p.rowid`)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		var records []model.Record
		for rows.Next() {
			var r model.Record
			var raw []byte
			if err := rows.Scan(&r.ID, &r.Version, &raw); err != nil {
				return err
			}
			r.Data = raw
			records = append(records, r)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, r := range records {
			p, err := Decode[model.ExecutionPlan](r)
			if err != nil {
				return err
			}
			if err := insertRoleTaskTx(ctx, tx, r.ID, r.Version, p, ""); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) PushRoleTasks(ctx context.Context, role taskqueue.Role, p taskqueue.Push) error {
	if !role.Valid() || (p.PlanID == "" && p.Version != 0) || (p.PlanID != "" && p.Version < 1) {
		return taskqueue.ErrConflict
	}
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		if p.PlanID == "" {
			_, err := tx.ExecContext(ctx, "UPDATE role_tasks SET ready=1 WHERE role=? AND ready=0", role)
			return err
		}
		r := model.Record{ID: p.PlanID}
		var raw []byte
		err := tx.QueryRowContext(ctx, "SELECT version,data FROM resources WHERE kind='execution_plans' AND id=?", p.PlanID).Scan(&r.Version, &raw)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && r.Version != p.Version) {
			return taskqueue.ErrConflict
		}
		if err != nil {
			return err
		}
		r.Data = raw
		plan, err := Decode[model.ExecutionPlan](r)
		if err != nil {
			return err
		}
		if err := insertRoleTaskTx(ctx, tx, r.ID, r.Version, plan, role); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE role_tasks SET ready=1 WHERE role=? AND plan_id=? AND version=?", role, p.PlanID, p.Version)
		return err
	})
}

// PopRoleTask atomically leases the oldest available delivery. An in-flight
// head is not overtaken. Expired heads can be delivered with a new token.
func (s *Store) PopRoleTask(ctx context.Context, role taskqueue.Role) (*taskqueue.Delivery, error) {
	if !role.Valid() {
		return nil, taskqueue.ErrConflict
	}
	var result *taskqueue.Delivery
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		result = nil
		now := time.Now()
		d, err := popEligibleRoleHead(ctx, tx, role, now.UnixMilli())
		if err != nil || d == nil {
			return err
		}
		d.Token, d.ExpiresAt = ID(), now.Add(roleLease).UnixMilli()
		write, err := tx.ExecContext(ctx, "UPDATE role_tasks SET token=?,expires_at=? WHERE id=? AND expires_at<=?", d.Token, d.ExpiresAt, d.ID, now.UnixMilli())
		if err != nil {
			return err
		}
		if err := requireOneRow(write); err != nil {
			return err
		}
		result = d
		return nil
	})
	return result, err
}

func popEligibleRoleHead(ctx context.Context, tx *sql.Tx, role taskqueue.Role, now int64) (*taskqueue.Delivery, error) {
	// A changed project policy can invalidate a critical approval without
	// advancing the plan revision. Retire that stale head, not the next one.
	for range 32 {
		var d taskqueue.Delivery
		var plan model.ExecutionPlan
		var raw []byte
		var ready int
		var hash string
		err := tx.QueryRowContext(ctx, `SELECT q.id,q.plan_id,q.version,q.stage,q.expires_at,q.ready,t.data,
		  COALESCE(json_extract(p.data,'$.policy_hash'),'')
		FROM role_tasks q
		JOIN resources p ON p.kind='projects' AND p.id=q.project_id
		JOIN resources t ON t.kind='execution_plans' AND t.id=q.plan_id AND t.version=q.version
		WHERE role=? AND COALESCE(json_extract(p.data,'$.paused'),0)=0
		  AND NOT EXISTS (SELECT 1 FROM resources WHERE kind='settings' AND id='global' AND json_extract(data,'$.stopped')=1)
		ORDER BY q.id LIMIT 1`, role).Scan(&d.ID, &d.PlanID, &d.Version, &d.Stage, &d.ExpiresAt, &ready, &raw, &hash)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &plan); err != nil {
			return nil, err
		}
		if plan.Stage != d.Stage || taskqueue.ForPlan(plan, hash) != role {
			if _, err := tx.ExecContext(ctx, "DELETE FROM role_tasks WHERE id=?", d.ID); err != nil {
				return nil, err
			}
			continue
		}
		if ready == 0 || d.ExpiresAt > now {
			return nil, nil
		}
		return &d, nil
	}
	return nil, nil
}

func (s *Store) SettleRoleTask(ctx context.Context, role taskqueue.Role, receipt taskqueue.Receipt, action string) error {
	if !role.Valid() || receipt.ID <= 0 || len(receipt.Token) != 24 {
		return taskqueue.ErrConflict
	}
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		var roleName taskqueue.Role
		var token string
		var expires int64
		err := tx.QueryRowContext(ctx, "SELECT role,token,expires_at FROM role_tasks WHERE id=?", receipt.ID).Scan(&roleName, &token, &expires)
		if errors.Is(err, sql.ErrNoRows) && (action == "ack" || action == "release") {
			return nil // the state transition has already settled the row
		}
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return taskqueue.ErrConflict
			}
			return err
		}
		now := time.Now()
		if roleName != role || token != receipt.Token || expires <= now.UnixMilli() {
			return taskqueue.ErrConflict
		}
		switch action {
		case "renew":
			_, err = tx.ExecContext(ctx, "UPDATE role_tasks SET expires_at=? WHERE id=?", now.Add(roleLease).UnixMilli(), receipt.ID)
		case "release":
			_, err = tx.ExecContext(ctx, "UPDATE role_tasks SET expires_at=0,token='' WHERE id=?", receipt.ID)
		case "ack":
			// A successful workflow transition deletes the old row atomically.
			// Acknowledging a still-current row would silently discard work.
			return taskqueue.ErrConflict
		default:
			return taskqueue.ErrConflict
		}
		return err
	})
}
