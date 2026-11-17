package provider

import (
	"os/exec"

	"whirlwind/internal/model"
)

// Defaults describes built-in provider commands and capabilities, including the explicitly unavailable Antigravity entry.
func Defaults() []model.Provider {
	return []model.Provider{
		defaultACPProvider("codex", "Codex", "quota"),
		defaultACPProvider("cursor", "Cursor"),
		defaultACPProvider("claude", "Claude"),
		{
			ID: "antigravity", Name: "Antigravity", Kind: "unavailable",
			Capabilities: []string{}, Quota: "unknown",
			Limitation: "Consumer-plan third-party application wrapping unsupported; supported integration required.",
		},
	}
}

func defaultACPProvider(id, name string, extraCapabilities ...string) model.Provider {
	capabilities := append(model.AgentRoles(), "submit", "resume", "permissions")
	return model.Provider{
		ID: id, Name: name, Kind: "acp", ACPProfile: id,
		Command:      ACPCommand(model.Provider{Kind: "acp", ACPProfile: id}),
		Capabilities: append(capabilities, extraCapabilities...),
		Quota:        "unknown",
	}
}

// Available checks that a supported provider command executable exists; it does not prove authentication or quota.
func Available(p model.Provider) bool {
	if p.Kind == "unavailable" || len(p.Command) == 0 {
		return false
	}
	_, e := exec.LookPath(p.Command[0])
	return e == nil
}
