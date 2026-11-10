package execution

import (
	"slices"
	"strings"
)

func filteredEnvironment(environment []string, profile Profile) []string {
	var filtered []string
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if environmentAllowed(name, profile) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func openShellBootstrapEnvironment(name string) bool {
	return slices.Contains([]string{"WHIRLWIND_OPENSHELL_CLI", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "OPENSHELL_GATEWAY", "OPENSHELL_GATEWAY_ENDPOINT", "OPENSHELL_WORKSPACE"}, name)
}

func environmentAllowed(name string, profile Profile) bool {
	if name == "WHIRLWIND_MCP_BROKER_TOKEN" {
		return false
	}
	if strings.HasPrefix(name, "WHIRLWIND_PROCESS_") {
		return true
	}
	if slices.Contains([]string{"PATH", "HOME", "USER", "LOGNAME", "LANG", "LC_ALL", "TERM", "TMPDIR", "WHIRLWIND_OPENSHELL_IMAGE"}, name) {
		return true
	}
	return runtimeEnvironmentAllowed(name, profile) || policyEnvironmentAllowed(name, profile)
}
func runtimeEnvironmentAllowed(name string, profile Profile) bool {
	if IsNonoLauncher(profile.Launcher) {
		return name == "WHIRLWIND_NONO_CLI"
	}
	return openShellLauncher(profile.Launcher) && openShellBootstrapEnvironment(name)
}
func policyEnvironmentAllowed(name string, profile Profile) bool {
	return slices.Contains(profile.Environment, name) && (!openShellLauncher(profile.Launcher) || !strings.HasPrefix(name, "OPENSHELL_"))
}
