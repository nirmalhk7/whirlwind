package model

import (
	"fmt"
	"path/filepath"
	"regexp"
)

var offlineImageRef = regexp.MustCompile(`^[a-z0-9][a-z0-9._:/-]*@sha256:[0-9a-f]{64}$`)

func IsOfflineDockerImage(reference string) bool { return offlineImageRef.MatchString(reference) }

func ValidateOfflineDockerCheck(check Check) error {
	if len(check.Command) == 0 || check.Mode == "autofix" || len(check.OutputPaths) != 0 || len(check.Environment) != 0 || check.ReportFormat == "sarif" || filepath.Base(check.Command[0]) == "qlty" || (check.Required != nil && !*check.Required) {
		return fmt.Errorf("offline Docker check %s requires a mandatory read-only command without host environment, scanner, or output grants", check.Name)
	}
	return nil
}
