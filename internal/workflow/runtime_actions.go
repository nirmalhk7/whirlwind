package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

var ErrActionOutcomeUncertain = errors.New("tool action outcome is uncertain")

type uncertainToolAction struct {
	ID     string
	Method string
}

type toolActionEvent struct {
	ActionID        string `json:"action_id"`
	Method          string `json:"method"`
	Status          string `json:"status"`
	TerminalID      string `json:"terminal_id"`
	TerminalIDCamel string `json:"terminalId"`
	CleanupVerified bool   `json:"cleanup_verified"`
}

func unresolvedToolActions(events []model.AttemptEvent) []uncertainToolAction {
	tracker := toolActionTracker{active: map[string]uncertainToolAction{}, terminals: map[string]string{}}
	for _, event := range events {
		var value toolActionEvent
		if json.Unmarshal(event.Metadata, &value) != nil {
			continue
		}
		tracker.record(event, value)
	}
	result := make([]uncertainToolAction, 0, len(tracker.active))
	for _, action := range tracker.active {
		result = append(result, action)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

type toolActionTracker struct {
	active    map[string]uncertainToolAction
	terminals map[string]string
}

func (tracker toolActionTracker) record(event model.AttemptEvent, value toolActionEvent) {
	switch event.Kind {
	case "whirlwind/action_started":
		id := value.ActionID
		if id == "" {
			id = fmt.Sprintf("event-%d", event.Sequence)
		}
		tracker.active[id] = uncertainToolAction{ID: id, Method: value.Method}
	case "whirlwind/action_result":
		tracker.resolveAction(value)
	case "whirlwind/terminal/exit":
		tracker.resolveTerminal(value)
	}
}

func (tracker toolActionTracker) resolveAction(value toolActionEvent) {
	action, exists := tracker.active[value.ActionID]
	if !exists || value.Status != "completed" {
		return
	}
	if action.Method == "terminal/create" && value.TerminalID != "" {
		tracker.terminals[value.TerminalID] = action.ID
		return
	}
	delete(tracker.active, action.ID)
}

func (tracker toolActionTracker) resolveTerminal(value toolActionEvent) {
	terminalID := value.TerminalIDCamel
	if terminalID == "" {
		terminalID = value.TerminalID
	}
	if actionID := tracker.terminals[terminalID]; actionID != "" && value.CleanupVerified {
		delete(tracker.active, actionID)
		delete(tracker.terminals, terminalID)
	}
}

func (a *agentRun) reconcilePriorToolActions(ctx context.Context) error {
	records, err := a.Store.List(ctx, "sessions")
	if err != nil {
		return fmt.Errorf("load prior tool action history: %w", err)
	}
	for _, record := range records {
		attempt, err := store.Decode[model.Attempt](record)
		if err != nil {
			return err
		}
		if !sameRecoveryWorkItem(attempt, a) || attempt.AttemptID == "" {
			continue
		}
		events, err := a.Store.AttemptActionEvents(ctx, attempt.AttemptID)
		if err != nil {
			return fmt.Errorf("load action outcomes for attempt %s: %w", attempt.AttemptID, err)
		}
		if actions := unresolvedToolActions(events); len(actions) > 0 {
			return uncertainToolActionError(attempt, actions)
		}
	}
	return nil
}

func uncertainToolActionError(attempt model.Attempt, actions []uncertainToolAction) error {
	ids := make([]string, 0, len(actions))
	for _, action := range actions {
		ids = append(ids, action.ID+" ("+action.Method+")")
	}
	return fmt.Errorf("%w: attempt %s has unfinished action(s) %s; inspect the preserved worktree and terminal evidence before retrying", ErrActionOutcomeUncertain, attempt.AttemptID, strings.Join(ids, ", "))
}
