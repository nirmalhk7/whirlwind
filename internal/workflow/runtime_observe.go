package workflow

import (
	"encoding/json"
	"whirlwind/internal/model"
)

type runtimeTracker struct {
	Tools       map[string]model.ToolActivity
	Completed   map[string]bool
	Last        string
	Repetitions int
	Recover     bool
}

func (a *agentAttempt) observeRuntime(kind string, data []byte) error {
	if a.state.Runtime == nil {
		a.state.Runtime = &model.RuntimeSnapshot{}
	}
	switch kind {
	case "whirlwind/recovery_uncertain":
		a.state.Runtime.Uncertain = true
	case "whirlwind/capability_warning":
		var warning string
		_ = json.Unmarshal(data, &warning)
		if len(a.state.Runtime.Warnings) < 5 {
			a.state.Runtime.Warnings = append(a.state.Runtime.Warnings, warning)
		}
	case "whirlwind/context":
		var usage model.ContextUsage
		if err := json.Unmarshal(data, &usage); err != nil {
			return err
		}
		if usage.Used >= 0 && usage.Size > 0 && usage.Used <= usage.Size {
			a.state.Runtime.Context = &usage
		}
	case "whirlwind/tool":
		var tool model.ToolActivity
		if err := json.Unmarshal(data, &tool); err != nil {
			return err
		}
		a.observeTool(tool)
	}
	return nil
}

func (a *agentAttempt) observeTool(tool model.ToolActivity) {
	if a.tracker.Tools == nil {
		a.tracker.Tools = map[string]model.ToolActivity{}
		a.tracker.Completed = map[string]bool{}
	}
	a.tracker.Tools[tool.ID] = tool
	if a.tracker.Completed[tool.ID] || tool.Status != "failed" {
		return
	}
	a.tracker.Completed[tool.ID] = true
	if tool.InputHash == "" || tool.OutputHash == "" {
		return
	}
	progress := a.progressFingerprint()
	if progress == "" {
		return
	}
	identity := tool.Kind + ":" + tool.InputHash + ":" + tool.OutputHash + ":" + progress
	if identity == a.tracker.Last {
		a.tracker.Repetitions++
	} else {
		a.tracker.Last = identity
		a.tracker.Repetitions = 1
	}
	if a.tracker.Repetitions >= 3 {
		a.tracker.Recover = true
	}
}

func (a *agentAttempt) shouldStop() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ctx.Err() != nil || !a.tracker.Recover {
		return false
	}
	return !a.hasUnsettledTools()
}

// Caller holds a.mu.
func (a *agentAttempt) hasUnsettledTools() bool {
	for _, tool := range a.tracker.Tools {
		if tool.Status != "failed" && tool.Status != "completed" {
			return true
		}
	}
	return false
}

func (a *agentAttempt) recoveryOutcome(err error) error {
	if a.ctx.Err() != nil {
		return err
	}
	a.mu.Lock()
	pending := a.tracker.Recover
	uncertain := a.state.Runtime.Uncertain
	active := a.hasUnsettledTools()
	a.mu.Unlock()
	if uncertain || (active && (pending || err != nil)) {
		return a.pauseRecovery("Tool completion uncertain; inspect evidence before resuming")
	}
	if a.shouldStop() {
		return &recoveryRequired{}
	}
	return err
}
