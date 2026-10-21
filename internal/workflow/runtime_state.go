package workflow

import (
	"errors"
	"fmt"
	"strings"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
	"whirlwind/internal/store"
)

type runtimeState struct {
	Generation   int    `json:"generation"`
	Rotations    int    `json:"rotations"`
	CheckpointID string `json:"checkpoint_id"`
	Reason       string `json:"reason"`
}
type recoveryRequired struct{}

func (*recoveryRequired) Error() string { return "repeated failed actions require recovery" }

func (a *agentRun) runtimeScope() string {
	return fmt.Sprintf("%s:%d:%s:%s:%s", a.task.ID, a.task.PlanVersion, a.input.Role, a.input.UnitID, a.input.Purpose)
}

func (a *agentRun) loadRuntime() (runtimeState, int, error) {
	r, err := a.Store.Get(a.ctx, "runtime_states", a.runtimeScope())
	if errors.Is(err, store.ErrNotFound) {
		return runtimeState{}, 0, nil
	}
	if err != nil {
		return runtimeState{}, 0, err
	}
	state, err := store.Decode[runtimeState](r)
	return state, r.Version, err
}

func (a *agentRun) rotateSession(reason string, recovery bool) error {
	state, version, err := a.loadRuntime()
	if err != nil {
		return err
	}
	counts, countVersion, err := a.recoveryCount()
	if err != nil {
		return err
	}
	if limit := rotationLimit(state, counts, recovery); limit != "" {
		return a.pauseRecovery(limit)
	}
	id := store.ID()
	checkpoint := a.runtimeCheckpoint(reason)
	state.Generation++
	state.CheckpointID = id
	state.Reason = reason
	if recovery {
		counts++
	} else {
		state.Rotations++
	}
	changes := []store.Change{{Kind: "runtime_states", ID: a.runtimeScope(), Value: state, Expected: version}, {Kind: "runtime_checkpoints", ID: id, Value: checkpoint}, {Kind: "runtime_recoveries", ID: a.recoveryScope(), Value: counts, Expected: countVersion}}
	if err = a.Store.Write(a.ctx, changes, nil); err != nil {
		return fmt.Errorf("%w: recovery checkpoint: %v", store.ErrPersistence, err)
	}
	a.input.SessionID = ""
	a.resumeSource = ""
	a.appendRuntimeCheckpoint(jsonText(checkpoint))
	return nil
}

func (a *agentRun) recoveryScope() string {
	task := a.task.ID
	if strings.HasPrefix(task, ideationPrefix) {
		task = strings.TrimSuffix(task, ":novelty")
	}
	return fmt.Sprintf("%s:%d", task, a.task.PlanVersion)
}
func (a *agentRun) recoveryCount() (int, int, error) {
	r, err := a.Store.Get(a.ctx, "runtime_recoveries", a.recoveryScope())
	if errors.Is(err, store.ErrNotFound) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	count, err := store.Decode[int](r)
	return count, r.Version, err
}

func (a *agentRun) pauseRecovery(reason string) error {
	r, err := a.Store.Get(a.ctx, "tasks", a.task.ID)
	if err == nil {
		task, decodeErr := store.Decode[model.Task](r)
		if decodeErr != nil {
			return decodeErr
		}
		task.ResumeStage = task.Stage
		task.Stage = "paused"
		task.Error = reason
		err = a.Store.Put(a.ctx, "tasks", task.ID, task, r.Version)
	} else if errors.Is(err, store.ErrNotFound) {
		err = nil
	}
	if err != nil {
		return fmt.Errorf("%w: pause recovery: %v", store.ErrPersistence, err)
	}
	return errors.New(reason)
}

func highContext(usage *model.ContextUsage) bool {
	return usage != nil && usage.Size > 0 && float64(usage.Used)/float64(usage.Size) >= .85
}

func (a *agentRun) prepareRuntimeResume(saved storedSession) (bool, error) {
	state, _, err := a.loadRuntime()
	if err != nil {
		return false, err
	}
	generation := 0
	if saved.Runtime != nil {
		generation = saved.Runtime.Generation
	}
	if generation == state.Generation && saved.Runtime != nil && highContext(saved.Runtime.Context) {
		return false, a.rotateSession("Reported context usage reached 85%", false)
	}
	if err := a.attachRuntimeCheckpoint(state.CheckpointID); err != nil {
		return false, err
	}
	return generation == state.Generation && saved.Session != "", nil
}

func recoveryError(err error) bool { var required *recoveryRequired; return errors.As(err, &required) }
func (a *agentRun) retryRecovery(source model.Provider) (provider.Output, provider.FailureKind, *agentAttempt, error) {
	for {
		output, kind, execution, err := a.runSingleAttempt(source)
		if !recoveryError(err) {
			return output, kind, execution, err
		}
		if err = a.rotateSession("Repeated failed action with unchanged evidence", true); err != nil {
			return output, provider.FailureUnknown, execution, err
		}
	}
}

func rotationLimit(state runtimeState, recoveries int, recovery bool) string {
	if recovery && recoveries >= 2 {
		return "Automatic recovery exhausted after two restarts"
	}
	if !recovery && state.Rotations >= 3 {
		return "Context rotation allowance exhausted"
	}
	return ""
}
