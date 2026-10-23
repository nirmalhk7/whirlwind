//go:build !darwin && !linux

package provider

import (
	"os"
	"whirlwind/internal/execution"
)

func openACPFile(string, string, bool) (*os.File, error) {
	return nil, execution.ErrSandbox
}
