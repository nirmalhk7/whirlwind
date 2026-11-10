package execution

import (
	"fmt"
	"path/filepath"
	"time"
)

// StandaloneRuntimeProfile validates a registered locator and restores its exact pinned authority.
func StandaloneRuntimeProfile(root string, locator StandaloneRuntimeLocator) (Profile, error) {
	if !standaloneLocatorValid(root, locator) {
		return Profile{}, fmt.Errorf("%w: registered standalone runtime identity is incomplete or invalid", ErrIsolationUnverified)
	}
	return Profile{ExecutionID: locator.ExecutionID, ExecutionGeneration: locator.Generation, SandboxName: locator.SandboxName, SandboxID: locator.SandboxID,
		LifecycleFile: locator.LifecycleFile, GatewayName: locator.GatewayName, GatewayEndpoint: locator.GatewayEndpoint,
		Workspace: locator.Workspace, DeadlineAt: locator.DeadlineAt, CWD: locator.CWD, Launcher: locator.Launcher,
		SupervisorDir: root, ExecutionLabelValue: OpenShellExecutionLabel(locator.ExecutionID, locator.Generation), StandaloneRuntime: true}, nil
}

func standaloneAuthorityValid(locator StandaloneRuntimeLocator, native bool) bool {
	if native {
		return locator.GatewayName == "" && locator.GatewayEndpoint == "" && locator.Workspace == ""
	}
	return locator.GatewayName != "" && locator.GatewayEndpoint != "" && locator.Workspace != ""
}

func standaloneLocatorValid(root string, locator StandaloneRuntimeLocator) bool {
	return standalonePathsValid(root, locator) && standaloneExecutionValid(locator) && standaloneOwnerValid(locator) && standaloneRuntimePlacementValid(root, locator)
}
func standalonePathsValid(root string, locator StandaloneRuntimeLocator) bool {
	return filepath.IsAbs(root) && filepath.IsAbs(locator.Launcher) && filepath.IsAbs(locator.CWD) && filepath.Clean(locator.CWD) != string(filepath.Separator)
}
func standaloneExecutionValid(locator StandaloneRuntimeLocator) bool {
	return validExecutionID(locator.ExecutionID) && locator.Generation > 0 && locator.SandboxName == OpenShellSandboxName(locator.ExecutionID) && standaloneStateValid(locator.State)
}
func standaloneStateValid(state string) bool { return state == "active" || state == "cleanup_pending" }
func standaloneOwnerValid(locator StandaloneRuntimeLocator) bool {
	deadline, err := time.Parse(time.RFC3339Nano, locator.DeadlineAt)
	return err == nil && !deadline.IsZero() && locator.OwnerPID > 1 && locator.OwnerStarted != ""
}
func standaloneRuntimePlacementValid(root string, locator StandaloneRuntimeLocator) bool {
	filename := "openshell.json"
	native := locator.RuntimeKind == "nono"
	if native {
		filename = "nono.json"
		if !IsNonoLauncher(locator.Launcher) {
			return false
		}
	} else if locator.RuntimeKind != "openshell" || !IsOpenShellLauncher(locator.Launcher) {
		return false
	}
	return locator.LifecycleFile == filepath.Join(root, locator.ExecutionID, filename) && standaloneAuthorityValid(locator, native)
}
