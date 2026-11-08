package provider

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
)

// ProvisionNativeIntegrations declares bounded native files for the isolated
// provider bootstrap. It never changes network policy or grants an entire home.
func ProvisionNativeIntegrations(profile *execution.Profile, p model.Provider) error {
	if profile == nil {
		return errors.New("native integration execution profile required")
	}
	family, err := nativeProviderFamily(p)
	if err != nil {
		return err
	}
	home, err := nativeUserHome()
	if err != nil {
		return err
	}
	next, err := provisionNativeProfile(*profile, p, family, home)
	if err != nil {
		return err
	}
	*profile = next
	return nil
}

type nativeProvisioner struct {
	home    string
	profile *execution.Profile
}

func provisionNativeProfile(profile execution.Profile, p model.Provider, family, home string) (execution.Profile, error) {
	profile.NativeFiles = slices.Clone(profile.NativeFiles)
	profile.ReadRoots = slices.Clone(profile.ReadRoots)
	provisioner := nativeProvisioner{home: home, profile: &profile}
	if err := provisioner.files(family); err != nil {
		return profile, err
	}
	if err := provisioner.metadata(family); err != nil {
		return profile, err
	}
	if err := grantNativeMCPDefinitions(&profile, p, family); err != nil {
		return profile, err
	}
	slices.Sort(profile.ReadRoots)
	profile.ReadRoots = slices.Compact(profile.ReadRoots)
	return profile, nil
}

func (provisioner nativeProvisioner) add(relative, destinationHome, destinationPath string) error {
	source, exists, err := nativeRegularFile(provisioner.home, relative)
	if err != nil || !exists {
		return err
	}
	file := execution.NativeFile{Source: source, Home: destinationHome, Path: destinationPath}
	if !slices.Contains(provisioner.profile.NativeFiles, file) {
		provisioner.profile.NativeFiles = append(provisioner.profile.NativeFiles, file)
	}
	provisioner.profile.ReadRoots = append(provisioner.profile.ReadRoots, source)
	return nil
}

func (provisioner nativeProvisioner) files(family string) error {
	var files [][3]string
	switch family {
	case "codex":
		// .credentials.json is the installed Codex native MCP credential store.
		files = [][3]string{{".codex/config.toml", "CODEX_HOME", "config.toml"}, {".codex/auth.json", "CODEX_HOME", "auth.json"}, {".codex/.credentials.json", "CODEX_HOME", ".credentials.json"}}
	case "claude":
		files = [][3]string{{nativeClaudeUserConfig, "HOME", nativeClaudeUserConfig}, {nativeClaudeSettings, "CLAUDE_CONFIG_DIR", "settings.json"}, {".claude/.credentials.json", "CLAUDE_CONFIG_DIR", ".credentials.json"}}
	case "cursor":
		files = [][3]string{{nativeCursorMCPConfig, "HOME", nativeCursorMCPConfig}, {".cursor/cli-config.json", "HOME", ".cursor/cli-config.json"}}
	}
	for _, file := range files {
		if err := provisioner.add(file[0], file[1], file[2]); err != nil {
			return err
		}
	}
	return nil
}

func (provisioner nativeProvisioner) metadata(family string) error {
	switch family {
	case "codex":
		return provisionCodexPluginManifests(provisioner.home, provisioner.add)
	case "claude":
		return grantNativeProjectMCP(provisioner.profile)
	default:
		return nil
	}
}

func grantNativeProjectMCP(profile *execution.Profile) error {
	if profile.CWD == "" {
		return nil
	}
	project, err := filepath.EvalSymlinks(profile.CWD)
	if err != nil {
		return err
	}
	source, exists, err := nativeRegularFile(project, nativeProjectMCPConfig)
	if err != nil {
		return err
	}
	if exists {
		profile.ReadRoots = append(profile.ReadRoots, source)
	}
	return nil
}

func grantNativeMCPDefinitions(profile *execution.Profile, p model.Provider, family string) error {
	if family == "codex" {
		return nil
	}
	definitions, err := NativeMCPDefinitions(p, profile.CWD, profile.Environment)
	if err != nil {
		return err
	}
	for _, raw := range definitions {
		if err := grantNativeMCPCommand(profile, raw); err != nil {
			return err
		}
	}
	return nil
}

func nativeProviderFamily(p model.Provider) (string, error) {
	switch {
	case p.Kind == "acp" && slices.Contains([]string{"codex", "claude", "cursor"}, p.ACPProfile):
		return p.ACPProfile, nil
	case p.Kind == "codex" || p.ID == "codex" && p.Kind == "acp":
		return "codex", nil
	case p.Kind == "claude" || p.ID == "claude" && p.Kind == "acp":
		return "claude", nil
	case p.Kind == "acp" && (p.ID == "cursor" || len(p.Command) > 0 && slices.Contains([]string{"agent", "cursor-agent"}, filepath.Base(p.Command[0]))):
		return "cursor", nil
	default:
		return "", errors.New("native integrations require a supported Codex, Claude, or Cursor provider")
	}
}

func grantNativeMCPCommand(profile *execution.Profile, raw json.RawMessage) error {
	var server struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	if json.Unmarshal(raw, &server) != nil || server.Command == "" {
		return nil
	}
	command := server.Command
	if !filepath.IsAbs(command) && strings.ContainsRune(command, filepath.Separator) {
		command = filepath.Join(profile.CWD, command)
	}
	resolved, err := execution.ForCommand(*profile, append([]string{command}, server.Args...))
	if err != nil {
		return errors.New("native MCP command unavailable; install its configured executable before running integrations")
	}
	profile.ReadRoots = resolved.ReadRoots
	for _, argument := range server.Args {
		if path := nativeMCPScriptArgument(profile.CWD, argument); path != "" {
			profile.ReadRoots = append(profile.ReadRoots, path)
		}
	}
	return nil
}

func nativeMCPScriptArgument(cwd, argument string) string {
	if strings.HasPrefix(argument, "-") || !slices.Contains([]string{".js", ".mjs", ".cjs", ".py"}, filepath.Ext(argument)) {
		return ""
	}
	path := argument
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	entry, err := os.Lstat(path)
	if err != nil || !entry.Mode().IsRegular() {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	return resolved
}
