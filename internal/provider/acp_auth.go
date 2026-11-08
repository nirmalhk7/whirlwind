package provider

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
)

type acpAuthFile struct {
	source      string
	home        string
	destination string
}

var acpAuthFiles = map[string]acpAuthFile{
	"claude": {source: ".claude/.credentials.json", home: "CLAUDE_CONFIG_DIR", destination: ".credentials.json"},
	"codex":  {source: ".codex/auth.json", home: "CODEX_HOME", destination: "auth.json"},
	"cursor": {source: ".cursor/cli-config.json", home: "HOME", destination: ".cursor/cli-config.json"},
}

// provisionACPAuth grants only the selected ACP profile's existing auth file
// and already-authorized auth environment references. It never provisions
// integration manifests, plugin state, sessions, or an entire home directory.
func provisionACPAuth(profile *execution.Profile, provider model.Provider) error {
	if profile == nil {
		return errors.New("ACP authentication execution profile required")
	}
	selected, file, err := resolveACPAuthProfile(provider)
	if err != nil {
		return err
	}
	home, err := nativeUserHome()
	if err != nil {
		return fmt.Errorf("ACP provider %q authentication home unavailable: %w", provider.ID, err)
	}
	source, exists, err := nativeRegularFile(home, file.source)
	if err != nil {
		return fmt.Errorf("ACP provider %q authentication file must be a regular file without symlinks", provider.ID)
	}
	grants := authFileGrant(file, source, exists)
	availableEnvironment := authorizedACPEnvironment(profile.Environment, selected.AuthReferences)
	if len(grants) == 0 && len(availableEnvironment) == 0 {
		return fmt.Errorf("ACP provider %q has no authorized credential available; authenticate the selected profile or explicitly allow one of: %v", provider.ID, selected.AuthReferences)
	}
	applyACPAuthGrants(profile, grants)
	return nil
}

func resolveACPAuthProfile(provider model.Provider) (ACPProfile, acpAuthFile, error) {
	if provider.Kind != "acp" {
		return ACPProfile{}, acpAuthFile{}, errors.New("ACP authentication grants require an ACP provider")
	}
	selected, ok := ACPProfileByID(provider.ACPProfile)
	if !ok {
		return ACPProfile{}, acpAuthFile{}, fmt.Errorf("ACP provider %q has no supported authentication profile; select a bundled ACP profile", provider.ID)
	}
	file, ok := acpAuthFiles[selected.ID]
	if !ok {
		return ACPProfile{}, acpAuthFile{}, fmt.Errorf("ACP provider %q has no bounded authentication mapping", provider.ID)
	}
	return selected, file, nil
}

func authFileGrant(file acpAuthFile, source string, exists bool) []execution.NativeFile {
	if !exists {
		return nil
	}
	return []execution.NativeFile{{Source: source, Home: file.home, Path: filepath.ToSlash(file.destination)}}
}

func authorizedACPEnvironment(allowed, references []string) []string {
	var available []string
	for _, reference := range references {
		if !slices.Contains(allowed, reference) || !envReference(reference) {
			continue
		}
		if value, present := os.LookupEnv(reference); present && value != "" {
			available = append(available, reference)
		}
	}
	return available
}

func applyACPAuthGrants(profile *execution.Profile, grants []execution.NativeFile) {
	nextFiles := slices.Clone(profile.NativeFiles)
	nextRoots := slices.Clone(profile.ReadRoots)
	for _, grant := range grants {
		if !slices.Contains(nextFiles, grant) {
			nextFiles = append(nextFiles, grant)
		}
		if !slices.Contains(nextRoots, grant.Source) {
			nextRoots = append(nextRoots, grant.Source)
		}
	}
	profile.NativeFiles = nextFiles
	profile.ReadRoots = nextRoots
}

var acpEnvironmentReference = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

func envReference(name string) bool {
	return acpEnvironmentReference.MatchString(name)
}
