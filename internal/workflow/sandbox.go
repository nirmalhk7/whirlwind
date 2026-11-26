package workflow

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func (a *agentRun) prepareExecutionProfile(chosen model.Provider) error {
	effective := provider.ExecutionProvider(chosen, a.input.Thinking)
	policy, err := execution.CombineSandboxPolicies(a.project.Policy.Sandbox, chosen.Sandbox)
	if err != nil {
		return fmt.Errorf("sandbox setup required: %w", err)
	}
	profile, err := execution.ForProvider(a.input.CWD, a.input.ReadOnly, a.project.PolicyHash, policy, effective.Command)
	if err != nil {
		return fmt.Errorf("sandbox setup required: %w", err)
	}

	if err := configurePlannerProfile(&profile, a.input); err != nil {
		return err
	}

	profile.MCPBroker = a.input.MCPBroker
	if a.input.Role == "code" {
		profile.ReadRoots = append(profile.ReadRoots, a.autofixReadRoots...)
	}
	profile.MCPBrokerToken = a.input.MCPBrokerToken
	if a.input.NativeIntegrations {
		if execution.IsDirectLauncher(profile.Launcher) {
			return errors.New("native credential mounts require sandboxed execution")
		}
		if err := provisionNativeProfile(&profile, chosen); err != nil {
			return err
		}
	}
	profile.AllowedPaths = append([]string{}, a.project.Policy.AllowedPaths...)
	if err := a.protectHookSettings(&profile); err != nil {
		return err
	}
	a.input.ExecutionProfile = profile.Digest()
	scope := sha256.Sum256([]byte(a.task.ID + ":" + fmt.Sprint(a.task.PlanVersion) + ":" + a.input.Role + ":" + chosen.ID + ":" + a.input.Model + ":" + a.input.Purpose + ":" + a.input.UnitID + ":" + a.input.ExecutionProfile))
	profile.RuntimeDir = filepath.Join(a.Root, "sandbox", fmt.Sprintf("%x", scope[:]))
	if err = os.MkdirAll(profile.RuntimeDir, 0700); err != nil {
		return err
	}
	a.input.Execution = &profile
	return nil
}

func configurePlannerProfile(profile *execution.Profile, input provider.Input) error {

	if input.Purpose == "idea_planning" {
		if input.Role != "plan" || !input.ReadOnly {
			return fmt.Errorf("invalid output: Planner output requires read-only Planner role")
		}
		output, err := filepath.EvalSymlinks(input.PlannerOutputDirectory)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(profile.CWD, output)
		if err != nil || !filepath.IsLocal(relative) || relative == "." {
			return fmt.Errorf("invalid output: Planner output escapes assigned worktree")
		}
		profile.WriteRoots = []string{output}
	}
	return nil
}

func provisionNativeProfile(profile *execution.Profile, chosen model.Provider) error {
	if err := provider.ProvisionNativeIntegrations(profile, chosen); err != nil {
		return err
	}
	var digests []string
	for _, file := range profile.NativeFiles {
		raw, err := os.ReadFile(file.Source)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		digests = append(digests, fmt.Sprintf("%s:%x", file.Source, sum))
	}
	profile.NativeConfigDigest = evidenceDigest(digests)
	return nil
}

// The live settings file can overlap a provider's checkout when Whirlwind
// itself is registered as a project or uses a custom settings location.
func (a *agentRun) protectHookSettings(profile *execution.Profile) error {
	source, ok := a.Store.Source.(interface{ SettingsPath() string })
	if !ok {
		return nil
	}
	path := source.SettingsPath()
	directory, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return err
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	for _, candidate := range []string{filepath.Join(directory, filepath.Base(path)), canonical} {
		rel, err := filepath.Rel(profile.CWD, candidate)
		if err == nil && filepath.IsLocal(rel) && rel != "." && !slices.Contains(profile.ProtectedFiles, candidate) {
			if !execution.IsNonoLauncher(profile.Launcher) {
				return errors.New("authoritative settings inside the agent worktree require native nono protection")
			}
			profile.ProtectedFiles = append(profile.ProtectedFiles, candidate)
		}
	}
	return nil
}
