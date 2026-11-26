package model

import (
	"fmt"
	"path/filepath"
	"regexp"
)

var offlineImageRef = regexp.MustCompile(`^[a-z0-9][a-z0-9._:/-]*@sha256:[0-9a-f]{64}$`)

func IsOfflineDockerImage(reference string) bool { return offlineImageRef.MatchString(reference) }

const PinnedOfflineQltyCommand = "whirlwind-linux-qlty-probe"
const PinnedOfflineQltyImage = "golang@sha256:87c65a688975c0b1d4585d204874c45ca78354670e3a5f11368c5a492303d696"

func IsPinnedOfflineQlty(command []string) bool {
	return len(command) == 1 && command[0] == PinnedOfflineQltyCommand
}

func IsPinnedOfflineQltyCheck(check Check) bool {
	return check.Name == "qlty-offline" && IsPinnedOfflineQlty(check.Command) &&
		check.Mode == "" && check.ReportFormat == "sarif" && len(check.DependsOn) == 0 &&
		len(check.OutputPaths) == 0 && len(check.Environment) == 0 && !check.Reusable &&
		(check.Required == nil || *check.Required)
}

func ValidateOfflineDockerCheck(check Check) error {
	if IsPinnedOfflineQltyCheck(check) {
		return nil
	}
	if len(check.Command) > 0 && filepath.Base(check.Command[0]) == PinnedOfflineQltyCommand {
		return fmt.Errorf("invalid reserved offline scanner check %s", check.Name)
	}
	if len(check.Command) == 0 || check.Mode == "autofix" || len(check.OutputPaths) != 0 || len(check.Environment) != 0 || check.ReportFormat == "sarif" || filepath.Base(check.Command[0]) == "qlty" || (check.Required != nil && !*check.Required) {
		return fmt.Errorf("offline Docker check %s requires a mandatory read-only command without host environment, scanner, or output grants", check.Name)
	}
	return nil
}
