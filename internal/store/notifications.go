package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"whirlwind/internal/model"
)

func notifyTransaction(ctx context.Context, tx *sql.Tx, n model.Notification) error {
	sum := sha256.Sum256([]byte(n.TaskID + "\x00" + n.Kind + "\x00" + n.Message + "\x00" + n.Current + "\x00" + n.NextAction))
	n.ID = "notice-" + hex.EncodeToString(sum[:16])
	version := 0
	var raw []byte
	err := tx.QueryRowContext(ctx, "SELECT version,data FROM resources WHERE kind='inbox' AND id=?", n.ID).Scan(&version, &raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	n.FirstAt = now
	n.LastAt = now
	n.Occurrences = 1
	if version > 0 {
		var old model.Notification
		if err = json.Unmarshal(raw, &old); err != nil {
			return err
		}
		n.FirstAt = old.FirstAt
		n.Occurrences = old.Occurrences + 1
		n.Resolved = old.Resolved
	}
	return put(ctx, tx, "inbox", n.ID, n, version)
}

func (s *Store) stageNotification(ctx context.Context, n model.Notification) error {
	sum := sha256.Sum256([]byte(n.TaskID + "\x00" + n.Kind + "\x00" + n.Message + "\x00" + n.Current + "\x00" + n.NextAction))
	n.ID = "notice-" + hex.EncodeToString(sum[:16])
	r, err := s.Get(ctx, "inbox", n.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	n.FirstAt = time.Now().UTC().Format(time.RFC3339Nano)
	n.LastAt = n.FirstAt
	n.Occurrences = 1
	if r.Version > 0 {
		old, err := Decode[model.Notification](r)
		if err != nil {
			return err
		}
		n.FirstAt = old.FirstAt
		n.Occurrences = old.Occurrences + 1
		n.Resolved = old.Resolved
	}
	return s.Put(ctx, "inbox", n.ID, n, r.Version)
}

func notifyTaskTransition(ctx context.Context, tx *sql.Tx, before, after []byte) error {
	var old, task model.Task
	if len(before) > 0 {
		if err := json.Unmarshal(before, &old); err != nil {
			return err
		}
	}
	if err := json.Unmarshal(after, &task); err != nil {
		return err
	}
	return notifyTaskStage(ctx, tx, old, task)
}

func notifyTaskStage(ctx context.Context, tx *sql.Tx, old, task model.Task) error {
	if task.Stage == old.Stage && task.Error == old.Error {
		return nil
	}
	messages := map[string]string{
		"awaiting_selection": "Idea awaits your selection before planning.",
		"planning":           "Planning selected idea within approved scope.",
		"plan_ready":         "Plan ready; preparing implementation.",
		"coding":             "Implementing approved objective.",
		"repair":             "Repairing failed checks or review findings.",
		"review":             "Reviewing implementation and verification evidence.",
		"waiting_quota":      "Task checkpointed while waiting for provider capacity.",
		"approval":           "Verified change needs policy-required publication approval.",
		"publishing":         "Verification accepted; publishing pull request.",
		"pr_created":         "Pull request ready for your review.",
		"complete":           "Task completed.",
		"paused":             "Task paused; retained work is available for inspection.",
		"questions":          "Task needs a scope decision.",
	}
	message, ok := messages[task.Stage]
	if !ok {
		return nil
	}
	if task.Error != "" {
		message += " " + task.Error
	}
	next := task.Handoff.NextAction
	if next == "" {
		next = map[string]string{"awaiting_selection": "Select the idea in Inbox.", "paused": "Inspect task details, resolve blocker, then resume.", "waiting_quota": "Metadata will be rechecked before resuming.", "pr_created": "Review the pull request; merge remains your decision.", "approval": "Inspect evidence and approve publication if appropriate."}[task.Stage]
	}
	if strings.TrimSpace(next) == "" {
		next = "Continue within approved scope and execution policy."
	}
	evidence := task.VerificationID
	if task.ReviewID != "" {
		evidence = task.ReviewID
	}
	usage, err := taskUsageTx(ctx, tx, task.ID)
	if err != nil {
		return err
	}
	usage.Entries = nil
	return notifyTransaction(ctx, tx, model.Notification{TaskID: task.ID, Kind: "task_" + task.Stage, Message: message, Current: task.Stage, NextAction: next, Completed: task.Handoff.CompletedWork, EvidenceID: evidence, Usage: &usage})
}
