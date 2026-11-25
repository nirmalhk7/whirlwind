package verification

import (
	"errors"

	"whirlwind/internal/model"
)

func validateQualityIdentityInput(output, runtimeDir string, snapshot *model.QualitySnapshot) error {
	switch {
	case snapshot == nil:
		return errors.New("frozen quality configuration is missing")
	case runtimeDir == "":
		return errors.New("isolated Qlty runtime directory is missing")
	case len(output) == 0:
		return errors.New("Qlty command produced no SARIF on stdout; inspect its saved stderr")
	case len(output) > 1<<20:
		return errors.New("Qlty SARIF exceeds the runtime identity evidence limit")
	default:
		return nil
	}
}

func qualityIdentityFailure(identityErr error, commandError string) string {
	reason := "quality runtime identity unavailable: " + identityErr.Error()
	if commandError != "" {
		return reason + " (command: " + commandError + ")"
	}
	return reason
}
