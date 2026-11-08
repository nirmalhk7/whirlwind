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
	active := map[string]uncertainToolAction{}
	terminals := map[string]string{}
	for _, event := range events {
		var value toolActionEvent
		if json.Unmarshal(event.Metadata, &value) != nil {
			continue
		}
		switch event.Kind {
		case "whirlwind/action_started":
			id := value.ActionID
			if id == "" {
				id = fmt.Sprintf("event-%d", event.Sequence)
			}
			active[id] = uncertainToolAction{ID: id, Method: value.Method}
		case "whirlwind/action_result":
			action, exists := active[value.ActionID]
			if !exists {
				continue
			}
			if action.Method == "terminal/create" && value.TerminalID != "" && value.Status == "completed" {
				terminals[value.TerminalID] = action.ID
				continue
			}
			if value.Status == "completed" {
				delete(active, action.ID)
			}
		case "whirlwind/terminal/exit":
			terminalID := value.TerminalIDCamel
			if terminalID == "" {
				terminalID = value.TerminalID
			}
			if actionID := terminals[terminalID]; actionID != "" && value.CleanupVerified {
				delete(active, actionID)
				delete(terminals, terminalID)
			}
		}
	}
	result := make([]uncertainToolAction, 0, len(active))
	for _, action := range active {
		result = append(result, action)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
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
