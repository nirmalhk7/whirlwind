package verification

import (
	"errors"
	"os"
	"os/exec"
)

// resolveJobCommand pins executable lookup to the job's environment and cwd.
// The approved command remains unchanged; arguments are never interpreted.
func resolveJobCommand(stage string, command JobCommand) ([]string, error) {
	if len(command.Command) == 0 {
		return nil, errors.New("verification command is empty")
	}
	pathValue, overridden := command.Environment["PATH"]
	if !overridden {
		pathValue = os.Getenv("PATH")
	}
	executable, err := lookPathInCWD(command.Command[0], pathValue, stage)
	if err != nil {
		return nil, err
	}
	// Explicit paths bypass PATH search, but still must name an executable file.
	executable, err = exec.LookPath(executable)
	if err != nil {
		return nil, err
	}
	return append([]string{executable}, command.Command[1:]...), nil
}
