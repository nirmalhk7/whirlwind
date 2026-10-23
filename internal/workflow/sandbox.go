package workflow

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"whirlwind/internal/execution"
	"whirlwind/internal/model"
	"whirlwind/internal/provider"
)

func (a *agentRun) prepareExecutionProfile(chosen model.Provider) error {
	effective := provider.ExecutionProvider(chosen, a.input.Thinking)
	policy := a.project.Policy.Sandbox
	if a.input.Purpose == "provider_test" && a.project.ID == "" {
		policy = chosen.Sandbox
	}
	profile, err := execution.ForProvider(a.input.CWD, a.input.ReadOnly, a.project.PolicyHash, policy, effective.Command)
	if err != nil {
		return fmt.Errorf("sandbox setup required: %w", err)
	}
	if a.input.NativeIntegrations {
		if err := provider.ProvisionNativeIntegrations(&profile, chosen); err != nil {
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
	}
	profile.AllowedPaths = append([]string{}, a.project.Policy.AllowedPaths...)
	a.input.ExecutionProfile = profile.Digest()
	scope := sha256.Sum256([]byte(a.task.ID + ":" + fmt.Sprint(a.task.PlanVersion) + ":" + a.input.Role + ":" + chosen.ID + ":" + a.input.Model + ":" + a.input.Purpose + ":" + a.input.UnitID + ":" + a.input.ExecutionProfile))
	profile.RuntimeDir = filepath.Join(a.Root, "sandbox", fmt.Sprintf("%x", scope[:]))
	if err = os.MkdirAll(profile.RuntimeDir, 0700); err != nil {
		return err
	}
	a.input.Execution = &profile
	return nil
}
