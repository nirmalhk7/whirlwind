package provider

import (
	"os/exec"
	"time"

	"whirlwind/internal/model"
)

// Defaults describes built-in provider commands and capabilities, including the explicitly unavailable Antigravity entry.
func Defaults(bridge string) []model.Provider {
	_ = bridge // Retained for callers from older configurations; Claude now uses its bundled ACP profile.
	return []model.Provider{{ID: "codex", Name: "Codex", Kind: "acp", ACPProfile: "codex", Command: ACPCommand(model.Provider{Kind: "acp", ACPProfile: "codex"}), Capabilities: []string{"ideate", "plan", "code", "review", "finalizer", "submit", "resume", "permissions", "quota"}, Quota: "unknown"}, {ID: "cursor", Name: "Cursor", Kind: "acp", ACPProfile: "cursor", Command: ACPCommand(model.Provider{Kind: "acp", ACPProfile: "cursor"}), Capabilities: []string{"ideate", "plan", "code", "review", "finalizer", "submit", "resume", "permissions"}, Quota: "unknown"}, {ID: "claude", Name: "Claude", Kind: "acp", ACPProfile: "claude", Command: ACPCommand(model.Provider{Kind: "acp", ACPProfile: "claude"}), Capabilities: []string{"ideate", "plan", "code", "review", "finalizer", "submit", "resume", "permissions"}, Quota: "unknown"}, {ID: "antigravity", Name: "Antigravity", Kind: "unavailable", Capabilities: []string{}, Quota: "unknown", Limitation: "Consumer-plan third-party application wrapping unsupported; supported integration required."}}
}

// Available checks that a supported provider command executable exists; it does not prove authentication or quota.
func Available(p model.Provider) bool {
	if p.Kind == "unavailable" || len(p.Command) == 0 {
		return false
	}
	_, e := exec.LookPath(p.Command[0])
	return e == nil
}

func providerReady(p model.Provider) bool {
	if !p.Available || p.Kind == "unavailable" {
		return false
	}
	if p.Quota == "exhausted" {
		reset, e := time.Parse(time.RFC3339, p.ResetAt)
		if e != nil || time.Now().Before(reset) {
			return false
		}
	}
	return true
}
