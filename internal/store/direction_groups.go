package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"whirlwind/internal/model"
)

// Older generators saved four consecutive rows per project/day. Row order survives
// later selection edits, unlike updated_at. Persist that boundary once on upgrade.
func migrateDirectionGroups(tx *sql.Tx) error {
	entries, err := loadDirectionEntries(tx)
	if err != nil {
		return err
	}
	groups, err := assignLegacyDirectionGroups(tx, entries)
	if err != nil {
		return err
	}
	return keepLatestDirectionSelection(tx, groups)
}

type directionEntry struct {
	id, at    string
	direction model.Direction
}

func loadDirectionEntries(tx *sql.Tx) ([]directionEntry, error) {
	rows, err := tx.Query("SELECT id,data,updated_at FROM resources WHERE kind='directions' ORDER BY rowid")
	if err != nil {
		return nil, err
	}
	entries := []directionEntry{}
	for rows.Next() {
		var e directionEntry
		var data []byte
		if err = rows.Scan(&e.id, &data, &e.at); err != nil {
			break
		}
		if err = json.Unmarshal(data, &e.direction); err != nil {
			break
		}
		entries = append(entries, e)
	}
	rowErr := rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	if rowErr != nil {
		return nil, rowErr
	}
	return entries, nil
}

type legacyDirectionBatch struct {
	count int
	id    string
}

func assignLegacyDirectionGroups(tx *sql.Tx, entries []directionEntry) (map[string][]directionEntry, error) {
	legacy := map[string]legacyDirectionBatch{}
	groups := map[string][]directionEntry{}
	for _, e := range entries {
		d := &e.direction
		if d.GroupID == "" {
			key := d.ProjectID + "\x00" + d.Day
			b := legacy[key]
			if b.count%4 == 0 {
				b.id = "legacy-" + e.id
			}
			b.count++
			legacy[key] = b
			d.GroupID = b.id
			if _, err := tx.Exec("UPDATE resources SET data=json_set(data,'$.group_id',?) WHERE kind='directions' AND id=?", d.GroupID, e.id); err != nil {
				return nil, err
			}
		}
		key := d.ProjectID + "\x00" + d.GroupID
		groups[key] = append(groups[key], e)
	}
	return groups, nil
}

func keepLatestDirectionSelection(tx *sql.Tx, groups map[string][]directionEntry) error {
	for _, group := range groups {
		if err := keepLatestDirectionGroup(tx, group); err != nil {
			return err
		}
	}
	return nil
}

func keepLatestDirectionGroup(tx *sql.Tx, group []directionEntry) error {
	chosen := latestSelectedDirection(group)
	for i, entry := range group {
		if !entry.direction.Selected || i == chosen {
			continue
		}
		if _, err := tx.Exec("UPDATE resources SET data=json_set(data,'$.selected',json('false')),version=version+1 WHERE kind='directions' AND id=?", entry.id); err != nil {
			return err
		}
		if err := pauseLegacyDirectionTask(tx, "direction-"+entry.id); err != nil {
			return err
		}
	}
	return nil
}

func latestSelectedDirection(group []directionEntry) int {
	chosen := -1
	for i, entry := range group {
		if entry.direction.Selected && (chosen < 0 || entry.at > group[chosen].at) {
			chosen = i
		}
	}
	return chosen
}

func pauseLegacyDirectionTask(tx *sql.Tx, id string) error {
	var data []byte
	var version int
	err := tx.QueryRow("SELECT data,version FROM resources WHERE kind='tasks' AND id=?", id).Scan(&data, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var task model.Task
	if err = json.Unmarshal(data, &task); err != nil {
		return err
	}
	if task.Stage == "complete" || task.Stage == "pr_created" || (task.Stage == "paused" && task.Error != "Paused by global runner stop") {
		return nil
	}
	if task.Stage != "paused" {
		task.ResumeStage = task.Stage
	}
	task.Stage = "paused"
	task.Error = "Direction deselected"
	return put(context.Background(), tx, "tasks", id, task, version)
}

type directionRemoval struct{}

// RemoveDirection participates in the caller's batch so task pauses, all four
// deletions, events and the idempotency receipt commit together.
func (s *Store) RemoveDirection(ctx context.Context, id string, expected int) error {
	if b := staged(ctx); b != nil {
		if err := b.put(ctx, s, Change{"directions", id, directionRemoval{}, expected}); err != nil {
			return err
		}
		b.records["directions:"+id] = model.Record{ID: id}
		return nil
	}
	return s.Transaction(ctx, func(tx *sql.Tx) error { return removeDirection(ctx, tx, id, expected) })
}

func removeDirection(ctx context.Context, tx *sql.Tx, id string, expected int) error {
	result, err := tx.ExecContext(ctx, "DELETE FROM resources WHERE kind='directions' AND id=? AND version=?", id, expected)
	if err != nil {
		return err
	}
	if err = requireOneRow(result); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO events(kind,resource_id,data,created_at) VALUES('directions',?,?,strftime('%Y-%m-%dT%H:%M:%fZ','now'))", id, `{"deleted":true}`)
	return err
}
