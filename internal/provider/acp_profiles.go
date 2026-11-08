package provider

import (
	"fmt"
	"slices"
	"strings"

	"whirlwind/internal/model"
)

// ACPProfile describes a bundled launch and configuration mapping. Model IDs
// and thinking values are discovered from the running agent, never cataloged here.
type ACPProfile struct {
	ID                     string
	Command                []string
	InstallHint            string
	AuthReferences         []string
	ModelConfigCategory    string
	ThinkingConfigCategory string
}

var bundledACPProfiles = []ACPProfile{
	{ID: "claude", Command: []string{"claude-agent-acp"}, InstallHint: "npm install --global @agentclientprotocol/claude-agent-acp@0.101.0", AuthReferences: []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"}, ModelConfigCategory: "model", ThinkingConfigCategory: "thought_level"},
	{ID: "codex", Command: []string{"codex-acp"}, InstallHint: "npm install --global @agentclientprotocol/codex-acp@2.5.0", AuthReferences: []string{"CODEX_API_KEY", "OPENAI_API_KEY"}, ModelConfigCategory: "model", ThinkingConfigCategory: "thought_level"},
	{ID: "cursor", Command: []string{"agent", "acp"}, InstallHint: "install Cursor CLI, then authenticate with agent login", AuthReferences: []string{"cursor_login", "CURSOR_API_KEY", "CURSOR_AUTH_TOKEN"}, ModelConfigCategory: "model", ThinkingConfigCategory: "thought_level"},
}

// ACPProfileByID returns a copy of a bundled ACP profile.
func ACPProfileByID(id string) (ACPProfile, bool) {
	for _, profile := range bundledACPProfiles {
		if profile.ID == id {
			profile.Command = slices.Clone(profile.Command)
			profile.AuthReferences = slices.Clone(profile.AuthReferences)
			return profile, true
		}
	}
	return ACPProfile{}, false
}

// ResolveExecutionProvider supplies a bundled command for named ACP profiles
// and rejects records that still point at a native, non-ACP transport.
func ResolveExecutionProvider(p model.Provider) (model.Provider, error) {
	switch p.Kind {
	case "acp":
		if p.ACPProfile != "" {
			if _, ok := ACPProfileByID(p.ACPProfile); !ok {
				return p, fmt.Errorf("provider %s references unknown ACP profile %q", p.ID, p.ACPProfile)
			}
		}
		if len(p.Command) == 0 && p.ACPProfile != "" {
			profile, ok := ACPProfileByID(p.ACPProfile)
			if !ok {
				return p, fmt.Errorf("provider %s references unknown ACP profile %q", p.ID, p.ACPProfile)
			}
			p.Command = profile.Command
		}
		if len(p.Command) == 0 {
			return p, fmt.Errorf("provider %s has no ACP command; install an ACP agent or select a bundled profile", p.ID)
		}
		return p, nil
	case "codex", "claude":
		if len(p.ModelCommand) > 0 {
			profile := p.Kind
			p.Kind = "acp"
			p.Command = slices.Clone(p.ModelCommand)
			p.ACPProfile = profile
			return p, nil
		}
		return p, fmt.Errorf("provider %s uses unsupported native %s transport; configure an ACP agent command and set kind: acp", p.ID, p.Kind)
	default:
		return p, fmt.Errorf("provider %s has unsupported execution transport %q; configure an ACP agent", p.ID, p.Kind)
	}
}

// ACPProfileIDForCommand identifies a bundled command without accepting a
// command supplied by a different profile.
func ACPProfileIDForCommand(command []string) string {
	for _, profile := range bundledACPProfiles {
		if slices.Equal(command, profile.Command) {
			return profile.ID
		}
	}
	return ""
}

func actionableACPError(p model.Provider, err error) error {
	if err == nil {
		return nil
	}
	auth := ""
	if profile, ok := ACPProfileByID(p.ACPProfile); ok && len(profile.AuthReferences) > 0 {
		auth = "; use the existing provider login or authorized reference " + strings.Join(profile.AuthReferences, ", ")
	}
	install := "install the configured ACP runtime"
	if profile, ok := ACPProfileByID(p.ACPProfile); ok && profile.InstallHint != "" {
		install = profile.InstallHint
	}
	return fmt.Errorf("ACP provider %q could not start; %s and authenticate it%s: %w", p.ID, install, auth, err)
}

func authenticationACPError(p model.Provider, err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	if !strings.Contains(message, "auth") && !strings.Contains(message, "credential") && !strings.Contains(message, "unauthorized") {
		return err
	}
	auth := "use the existing provider login"
	if profile, ok := ACPProfileByID(p.ACPProfile); ok && len(profile.AuthReferences) > 0 {
		auth += " or authorized reference " + strings.Join(profile.AuthReferences, ", ")
	}
	return fmt.Errorf("ACP provider %q requires authentication; %s: %w", p.ID, auth, err)
}
