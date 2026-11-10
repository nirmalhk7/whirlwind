package execution

import (
	"errors"
	"fmt"
	"os"
	"whirlwind/internal/process"
)

// MarkStandaloneCleanupPending retains the locator when isolation verification fails.
func MarkStandaloneCleanupPending(profile Profile) error {
	owner, err := process.IdentityAt(os.Getpid())
	if err != nil || owner.Started == "" {
		return fmt.Errorf("%w: cannot verify standalone supervisor identity", ErrIsolationUnverified)
	}
	expected := standaloneLocator(profile)
	expected.OwnerPID, expected.OwnerStarted = owner.PID, owner.Started
	return updateStandaloneRegistry(profile.SupervisorDir, func(registry *standaloneRegistry) error {
		for index := range registry.Executions {
			if registry.Executions[index].ExecutionID == profile.ExecutionID {
				if !sameStandaloneAuthority(registry.Executions[index], expected) || registry.Executions[index].OwnerPID != expected.OwnerPID || registry.Executions[index].OwnerStarted != expected.OwnerStarted {
					return fmt.Errorf("%w: standalone runtime registry authority changed", ErrIsolationUnverified)
				}
				registry.Executions[index].State = "cleanup_pending"
				return nil
			}
		}
		return errors.New("standalone runtime registry entry is missing")
	})
}

// CompleteStandaloneRuntime removes only a matching locator after sandbox absence is verified.
func CompleteStandaloneRuntime(profile Profile) error {
	owner, err := process.IdentityAt(os.Getpid())
	if err != nil || owner.Started == "" {
		return fmt.Errorf("%w: cannot verify standalone supervisor identity", ErrIsolationUnverified)
	}
	expected := standaloneLocator(profile)
	expected.OwnerPID, expected.OwnerStarted = owner.PID, owner.Started
	return updateStandaloneRegistry(profile.SupervisorDir, func(registry *standaloneRegistry) error {
		for index, existing := range registry.Executions {
			if existing.ExecutionID != profile.ExecutionID {
				continue
			}
			if !sameStandaloneAuthority(existing, expected) || existing.OwnerPID != expected.OwnerPID || existing.OwnerStarted != expected.OwnerStarted {
				return fmt.Errorf("%w: standalone runtime registry authority changed", ErrIsolationUnverified)
			}
			registry.Executions = append(registry.Executions[:index], registry.Executions[index+1:]...)
			return nil
		}
		return errors.New("standalone runtime registry entry is missing")
	})
}
